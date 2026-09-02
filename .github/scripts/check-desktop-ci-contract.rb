#!/usr/bin/env ruby
# frozen_string_literal: true

require "yaml"

module DesktopCiContract
  module_function

  def load_workflow(directory, name)
    YAML.load_file(File.join(directory, name))
  end

  def needs(job)
    Array(job.fetch("needs"))
  end

  def windows_runner?(job)
    Array(job["runs-on"]).any? { |runner| runner.to_s.match?(/windows/i) }
  end

  def go_test_step(job)
    Array(job["steps"]).find { |step| step["run"].to_s.match?(/\bgo\s+test\b/) }
  end

  def assert(condition, message)
    raise message unless condition
  end

  # Every job that both runs on Windows and invokes `go test`. Structural, not
  # by name: the contract is about what executes, and a job renamed keeps its
  # obligations.
  def windows_go_jobs(jobs)
    jobs.select { |_name, job| windows_runner?(job) && go_test_step(job) }
  end

  LOCAL_CALL = %r{\A\./\.github/workflows/(?<file>[^@\s/]+\.ya?ml)(?:@\S+)?\z}

  # The reusable workflow in this repository that a job calls, if it calls one.
  # A `uses:` job has no `runs-on:` and no `steps:` of its own, so nothing that
  # reads a job body can see the suite from the caller: the edge has to be
  # followed to find out what running that job actually runs.
  def called_workflow(job)
    match = LOCAL_CALL.match(job["uses"].to_s)
    match && match[:file]
  end

  # Every call, out of one workflow's jobs, that reaches a Windows Go suite.
  # Keyed by the calling job, because the caller owes the obligations that live
  # on a call -- no condition, a place in `build.needs`. Valued by the callee,
  # because the suite owes the obligations that live on steps, and those are
  # asserted once, in the single file that describes them.
  def windows_go_calls(directory, jobs)
    jobs.each_with_object({}) do |(name, job), found|
      file = called_workflow(job)
      next unless file && File.exist?(File.join(directory, file))

      called = load_workflow(directory, file)
      suite = windows_go_jobs(called.fetch("jobs", {}))
      found[name] = [file, suite] unless suite.empty?
    end
  end

  # Scopes a permissions declaration grants at write. `write-all` is the one
  # shorthand that grants everything without naming a scope.
  def permission_writes(declared)
    return ["write-all (every scope)"] if declared.to_s == "write-all"
    return [] unless declared.is_a?(Hash)

    declared.select { |_scope, value| value.to_s == "write" }.keys
  end

  # Asserted identically wherever a Windows Go suite gates something -- the PR
  # workflow and the release workflow. Written once because two copies of a
  # rule are two rules, and the one that drifts is the one nobody reads.
  def assert_windows_go_suite(file, name, job)
    step = go_test_step(job)
    command = step.fetch("run").strip
    assert(command.match?(%r{(?:\A|\s)\./\.\.\.(?:\s|\z)}), "#{file} job #{name} must run the whole module on Windows: `go test` there names no ./... argument, so only a hand-picked package is covered (#{command})")
    assert(!command.match?(/\s-run[\s=]/), "#{file} job #{name} must not narrow the Windows Go suite with -run (#{command})")
    assert(command.include?("-tags vctestfixture"), "#{file} job #{name} must build the Windows Go suite with -tags vctestfixture, the same tag the ubuntu job uses, or the two runners compile different code (#{command})")
    assert(job["continue-on-error"] != true, "#{file} job #{name} must fail closed: continue-on-error on the job hides a red Windows suite")
    assert(step["continue-on-error"] != true, "#{file} job #{name} must fail closed: continue-on-error on its `go test` step hides a red Windows suite")
  end

  PINNED_ACTION = /\A[^@]+@[0-9a-f]{40}\z/

  # The suite itself, asserted where it is written rather than where it is
  # called. Everything here used to be asserted twice, once against the copy in
  # test.yml and once against the copy in release.yml; with one file called
  # from both, the strictness the release needed -- pinned actions, no `if:`,
  # the toolchain read from .go-version -- is what a branch push runs too, so
  # the two callers cannot be qualified by different code.
  def assert_windows_go_workflow(directory, file)
    workflow = load_workflow(directory, file)
    trigger = workflow["on"] || workflow[true]
    assert(trigger.is_a?(Hash) && trigger.key?("workflow_call"), "#{file} must be reusable through workflow_call, or the callers cannot reach it and the suite runs nowhere")

    suite = windows_go_jobs(workflow.fetch("jobs"))
    assert(!suite.empty?, "#{file} must declare a Windows `runs-on:` with a `run:` step invoking `go test`")

    # A called workflow with no `permissions:` block of its own does not run
    # unprivileged -- it INHERITS the caller's token, silently, and nothing goes
    # red. release.yml holds `contents: write`, `id-token: write` and
    # `attestations: write`, so the suite would run at every release with a
    # token that can push to this repository, mint an OIDC identity and sign
    # attestations, for a job whose whole purpose is to read the tree and run
    # tests. This is the very hole moving the suite out of release.yml was
    # supposed to close, reopened one level down.
    #
    # desktop-tests.yml, desktop-mac-app.yml and desktop-windows-app.yml each
    # declare `permissions:` with `contents: read` and nothing else, all three
    # for this reason. This is the fourth file reached the same way, and it is
    # held to the same form rather than allowed to be the exception -- a set of
    # four where three are alike is read at a glance; four different spellings
    # of read-only are read one by one, and eventually not at all.
    assert(workflow.key?("permissions"), "#{file} must declare a `permissions:` block of its own: a reusable workflow without one inherits the caller's token, and release.yml calls it holding contents: write, id-token: write and attestations: write, so the Windows suite would run every release able to push to this repository and to sign")
    declared = workflow.fetch("permissions")
    writes = permission_writes(declared) +
             suite.flat_map { |name, job| permission_writes(job["permissions"]).map { |scope| "#{scope} (on job #{name})" } }
    assert(writes.empty?, "#{file} must ask for read-only permissions, so a publish is refused by the token and not merely by us: it grants #{writes.sort.join(', ')} at write. A suite that checks out the tree and runs `go test` needs no token that can write to this repository, publish a package or sign anything")
    assert(declared == { "contents" => "read" }, "#{file} must declare the same read-only block its three sibling reusable workflows declare -- `permissions:` with `contents: read` and nothing else -- so all four called files can be read as alike (found #{declared.inspect})")

    suite.each do |name, job|
      assert_windows_go_suite(file, name, job)
      assert(!job.key?("if"), "#{file} job #{name} must use the default fail-closed success condition: any `if:` on the Windows Go gate -- `always()` above all -- lets the caller proceed while the suite is red")
      assert(!go_test_step(job).key?("if"), "#{file} job #{name} must not make its `go test` step conditional: a skipped step reports success and the gate passes without running anything")

      steps = Array(job["steps"])
      %w[actions/checkout actions/setup-go].each do |action|
        used = steps.select { |step| step["uses"].to_s.start_with?("#{action}@") }
        assert(used.size == 1, "#{file} job #{name} must use #{action} exactly once: found #{used.size}")
        reference = used.first.fetch("uses")
        assert(reference.match?(PINNED_ACTION), "#{file} job #{name} must pin #{action} to a commit SHA, not a moving tag: release.yml calls this file, and a release gate that resolves a tag at run time is a gate somebody else can move (#{reference})")
      end
      setup_go = steps.find { |step| step["uses"].to_s.start_with?("actions/setup-go@") }
      assert(setup_go.fetch("with", {})["go-version-file"] == ".go-version", "#{file} job #{name} must take its Go version from .go-version, or the Windows suite compiles with a different toolchain than every other job in this repository")
    end
  end

  def check(directory = File.expand_path("../workflows", __dir__))
    gate = load_workflow(directory, "desktop-tests.yml")
    trigger = gate["on"] || gate[true]
    assert(trigger.key?("workflow_call"), "desktop test gate must be reusable through workflow_call")
    assert(gate.fetch("permissions") == { "contents" => "read" }, "desktop test gate must remain read-only")

    jobs = gate.fetch("jobs")
    assert(jobs.keys == ["test"], "desktop test gate must have exactly one job")
    job = jobs.fetch("test")
    steps = job.fetch("steps")
    assert(steps.count { |step| step["uses"].to_s.start_with?("actions/checkout@") } == 1, "desktop test gate must check out once")
    assert(steps.count { |step| step["uses"].to_s.start_with?("actions/setup-node@") } == 1, "desktop test gate must set up Node once")
    assert(steps.count { |step| step["uses"].to_s.start_with?("actions/setup-go@") } == 1, "desktop test gate must set up Go for pinned VC provenance")
    commands = steps.filter_map { |step| step["run"] }
    provision = "desktop/scripts/provision-pinned-pi-smoke.sh"
    assert(commands.count(provision) == 1, "desktop test gate must provision and smoke the pinned runtime exactly once")
    assert(commands.count("npm test") == 1, "desktop test gate must run the full package test script exactly once")
    commands.each do |command|
      assert(!command.match?(/vitest.*(?:--exclude|--changed|--related)|npm test\s+--/), "desktop test gate must not filter the suite")
    end
    test_step = steps.find { |candidate| candidate["run"] == "npm test" }
    assert(test_step && test_step["working-directory"] == "desktop", "npm test must run in desktop")
    [provision, "npm test"].each do |command|
      step = steps.find { |candidate| candidate["run"] == command }
      assert(!step.key?("continue-on-error"), "#{command} must fail closed")
    end

    test_jobs = load_workflow(directory, "test.yml").fetch("jobs")
    assert(test_jobs.fetch("desktop-pinned-pi-smoke").fetch("uses") == "./.github/workflows/desktop-tests.yml", "PR workflow must call the desktop test gate")
    %w[desktop-mac-app desktop-windows-app].each do |name|
      package_job = test_jobs.fetch(name)
      assert(needs(package_job).include?("desktop-pinned-pi-smoke"), "PR #{name} must depend on desktop tests")
      assert(!package_job.key?("if"), "PR #{name} must not conditionally bypass desktop tests")
    end

    # Windows-only Go sources exist (internal/harness/cmdline_windows_test.go,
    # internal/update/replace_windows_test.go) and no workflow executed them:
    # every `go test` in this repository ran on ubuntu-latest, and GOOS=windows
    # was compiled only inside release.yml's cross-build matrix. A defect in
    # Windows-only code therefore survived every branch and every pull request
    # and surfaced at release.
    #
    # The suite closing that is described once, in a reusable workflow, and
    # both test.yml and release.yml reach it through `uses:` -- the arrangement
    # desktop packaging already uses, and for the same reason: a green run on a
    # branch is evidence about the release only when the steps are not similar
    # but the same file. A copy inside release.yml would be a third description,
    # reachable from no branch push, free to drift, and the copy that drifts is
    # always the one nobody runs. So this walks the call edge rather than
    # looking for a Windows job in the caller: a `uses:` job has no `runs-on:`
    # and no steps, and reading only job bodies would see nothing at all.
    pr_windows_calls = windows_go_calls(directory, test_jobs)
    assert(!pr_windows_calls.empty?, "test.yml must reach the Windows Go suite through `uses:`: no job there calls a reusable workflow in this repository that declares a Windows `runs-on:` with a `run:` step invoking `go test`, so every *_windows_test.go in this repository executes nowhere")
    pr_windows_calls.each do |name, (file, _suite)|
      call = test_jobs.fetch(name)
      assert(!call.key?("if"), "PR #{name} must not conditionally bypass the Windows Go suite in #{file}")
    end

    release_jobs = load_workflow(directory, "release.yml").fetch("jobs")
    assert(release_jobs.fetch("desktop-pinned-pi-smoke").fetch("uses") == "./.github/workflows/desktop-tests.yml", "release workflow must call the desktop test gate")
    assert(needs(release_jobs.fetch("build")).include?("desktop-pinned-pi-smoke"), "release CLI builds must depend on desktop tests")

    # The Windows Go suite gated pull requests and gated nothing that publishes:
    # release.yml ran one `go test`, on ubuntu-latest, and the chain
    # `test + desktop-pinned-pi-smoke -> build -> release -> publish-auth` never
    # mentioned Windows. So a Windows-only defect could go red on the pull
    # request, be merged anyway or arrive by a path with no pull request, and
    # ship in the Windows binary and the Windows installer with every gate
    # green. The call asserted here closes that, and closes it with the very
    # file the branch push runs: no condition on the call, and named in
    # `build.needs` so publication cannot outrun it.
    release_windows_calls = windows_go_calls(directory, release_jobs)
    assert(!release_windows_calls.empty?, "release.yml must reach the Windows Go suite through `uses:` before it publishes: no job there calls a reusable workflow in this repository that declares a Windows `runs-on:` with a `run:` step invoking `go test`. Publication depends on `test` (ubuntu-latest) and `desktop-pinned-pi-smoke` only, so a Windows-only failure the PR workflow catches still reaches a released Windows binary")
    release_windows_calls.each do |name, (file, _suite)|
      call = release_jobs.fetch(name)
      assert(!call.key?("if"), "release.yml job #{name} must use the default fail-closed success condition: any `if:` on the call to #{file} -- `always()` above all -- lets the release proceed while the suite is red, or skips it outright")
    end

    # The same file, not merely a file of the same shape in each. Two reusable
    # workflows would restore exactly what moving the suite out of release.yml
    # removed: a release qualified by a description no branch push ever runs.
    shared = pr_windows_calls.values.map(&:first) & release_windows_calls.values.map(&:first)
    assert(!shared.empty?, "test.yml and release.yml must call the SAME Windows Go workflow: test.yml reaches #{pr_windows_calls.values.map(&:first).uniq.inspect} and release.yml reaches #{release_windows_calls.values.map(&:first).uniq.inspect}. Two files are two descriptions, and a green branch run then says nothing about what qualifies the release")

    (pr_windows_calls.values.map(&:first) | release_windows_calls.values.map(&:first)).each do |file|
      assert_windows_go_workflow(directory, file)
    end

    missing_windows_gate = release_windows_calls.keys - needs(release_jobs.fetch("build"))
    assert(missing_windows_gate.empty?, "release.yml build must depend on every job that calls the Windows Go suite, alongside test and desktop-pinned-pi-smoke: build.needs is #{needs(release_jobs.fetch('build')).inspect} and does not name #{missing_windows_gate.inspect}, so the suite runs beside the release instead of before it")
    %w[desktop-mac-app desktop-windows-app].each do |name|
      package_job = release_jobs.fetch(name)
      assert(needs(package_job).include?("desktop-pinned-pi-smoke"), "release #{name} must depend on desktop tests")
      assert(package_job.fetch("if") == "${{ vars.DESKTOP_RELEASE == 'true' }}", "release #{name} may only use the desktop opt-in condition")
    end
    publication = release_jobs.fetch("release")
    assert(needs(publication) == ["build"], "CLI publication must depend directly on desktop-qualified builds")
    assert(!publication.key?("if"), "CLI publication must use the default fail-closed success condition")
    attach = release_jobs.fetch("desktop-attach")
    assert(needs(attach).sort == ["desktop-mac-app", "desktop-windows-app", "release"], "desktop publication must depend on CLI publication and both qualified packages")
    assert(attach.fetch("if") == "${{ vars.DESKTOP_RELEASE == 'true' }}", "desktop publication may only use the desktop opt-in condition")
    assert(needs(release_jobs.fetch("publish-auth")) == ["release"], "auth publication must depend on qualified CLI publication")
    true
  end
end

if $PROGRAM_NAME == __FILE__
  DesktopCiContract.check(ENV.fetch("WORKFLOW_DIR", File.expand_path("../workflows", __dir__)))
  puts "desktop Vitest is a fail-closed PR, packaging, and release gate"
end
