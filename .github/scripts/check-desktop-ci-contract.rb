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
    # and surfaced at release. These assertions say the Windows run exists, is
    # the whole module, is built the way ubuntu builds it, and is allowed to
    # fail the run.
    pr_windows_go = windows_go_jobs(test_jobs)
    assert(!pr_windows_go.empty?, "test.yml must run the Go suite on Windows: no job declares a Windows `runs-on:` with a `run:` step invoking `go test`, so every *_windows_test.go in this repository executes nowhere")
    pr_windows_go.each { |name, job| assert_windows_go_suite("test.yml", name, job) }

    release_jobs = load_workflow(directory, "release.yml").fetch("jobs")
    assert(release_jobs.fetch("desktop-pinned-pi-smoke").fetch("uses") == "./.github/workflows/desktop-tests.yml", "release workflow must call the desktop test gate")
    assert(needs(release_jobs.fetch("build")).include?("desktop-pinned-pi-smoke"), "release CLI builds must depend on desktop tests")

    # The Windows Go suite exists in test.yml and is real, but it gated nothing
    # that publishes: release.yml ran one `go test`, on ubuntu-latest, and the
    # chain `test + desktop-pinned-pi-smoke -> build -> release -> publish-auth`
    # never mentioned Windows. So a Windows-only defect could go red on the pull
    # request, be merged anyway or arrive by a path with no pull request, and
    # ship in the Windows binary and the Windows installer with every gate
    # green. The asserted job closes that: same runner class, same whole-module
    # command, same fixture tag, pinned the way the release pins everything it
    # trusts, and named in `build.needs` so publication cannot outrun it.
    release_windows_go = windows_go_jobs(release_jobs)
    assert(!release_windows_go.empty?, "release.yml must run the Go suite on Windows before it publishes: no job there declares a Windows `runs-on:` with a `run:` step invoking `go test`. Publication depends on `test` (ubuntu-latest) and `desktop-pinned-pi-smoke` only, so a Windows-only failure the PR workflow catches still reaches a released Windows binary")
    release_windows_go.each do |name, job|
      assert_windows_go_suite("release.yml", name, job)
      assert(!job.key?("if"), "release.yml job #{name} must use the default fail-closed success condition: any `if:` on the Windows Go gate -- `always()` above all -- lets the release proceed while the suite is red")
      assert(!go_test_step(job).key?("if"), "release.yml job #{name} must not make its `go test` step conditional: a skipped step reports success and the gate passes without running anything")
      steps = Array(job["steps"])
      %w[actions/checkout actions/setup-go].each do |action|
        used = steps.select { |step| step["uses"].to_s.start_with?("#{action}@") }
        assert(used.size == 1, "release.yml job #{name} must use #{action} exactly once: found #{used.size}")
        reference = used.first.fetch("uses")
        assert(reference.match?(PINNED_ACTION), "release.yml job #{name} must pin #{action} to a commit SHA, not a moving tag, the way the Windows job in test.yml does -- a release gate that resolves a tag at run time is a gate somebody else can move (#{reference})")
      end
      setup_go = steps.find { |step| step["uses"].to_s.start_with?("actions/setup-go@") }
      assert(setup_go.fetch("with", {})["go-version-file"] == ".go-version", "release.yml job #{name} must take its Go version from .go-version, or the release Windows suite compiles with a different toolchain than every other job in this repository")
    end
    missing_windows_gate = release_windows_go.keys - needs(release_jobs.fetch("build"))
    assert(missing_windows_gate.empty?, "release.yml build must depend on every Windows Go suite in the file, alongside test and desktop-pinned-pi-smoke: build.needs is #{needs(release_jobs.fetch('build')).inspect} and does not name #{missing_windows_gate.inspect}, so the suite runs beside the release instead of before it")
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
