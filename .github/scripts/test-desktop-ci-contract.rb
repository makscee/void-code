#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "tmpdir"
require "yaml"
require_relative "check-desktop-ci-contract"

SOURCE = File.expand_path("../workflows", __dir__)

def assert_rejected(label)
  Dir.mktmpdir("desktop-ci-contract-") do |directory|
    FileUtils.cp_r("#{SOURCE}/.", directory)
    yield directory
    begin
      DesktopCiContract.check(directory)
    rescue StandardError
      next
    end
    abort "mutation survived: #{label}"
  end
end

# The unmutated tree has to pass first: every mutation below is only evidence
# that the check is strict, and a check that rejects the real workflows too
# rejects everything for free.
DesktopCiContract.check(SOURCE)

# The mutations below key off structure, never off literal text. The Windows Go
# suite is written by whoever implements it, in a file this script does not name
# and under a job name it does not know, so text that guessed either would stop
# matching the moment they changed -- and a mutation that matches nothing is a
# mutation that always "survives" nothing, which is the failure mode that makes
# a file like this decorative. Every locator below raises when it cannot find
# its target, so the vacuous case aborts instead of passing.
#
# The suite is reached through `uses:`, so a mutation has two places to bite:
# the call, which lives in the caller, and the suite, which lives in the file
# called. A call site hands the block both, already parsed.
CallSite = Struct.new(:caller_path, :caller_workflow, :jobs, :job_name, :job,
                      :callee_path, :callee_workflow, :suite_name, :suite_job, :go_step)

def windows_go_call_site(directory, file, label)
  caller_path = File.join(directory, file)
  caller_workflow = YAML.load_file(caller_path)
  jobs = caller_workflow.fetch("jobs")
  job_name, (callee_file, _suite) = DesktopCiContract.windows_go_calls(directory, jobs).first
  raise "fixture missing: #{file} has no job calling a reusable Windows Go workflow (#{label})" unless job_name

  callee_path = File.join(directory, callee_file)
  callee_workflow = YAML.load_file(callee_path)
  suite_name, suite_job = DesktopCiContract.windows_go_jobs(callee_workflow.fetch("jobs")).first
  raise "fixture missing: #{callee_file} has no Windows job running go test (#{label})" unless suite_job

  CallSite.new(caller_path, caller_workflow, jobs, job_name, jobs.fetch(job_name),
               callee_path, callee_workflow, suite_name, suite_job,
               DesktopCiContract.go_test_step(suite_job))
end

# Both files are written back, because a block is free to touch either side.
def mutate_windows_go_call(directory, label, file = "test.yml")
  site = windows_go_call_site(directory, file, label)
  yield site
  File.write(site.caller_path, YAML.dump(site.caller_workflow))
  File.write(site.callee_path, YAML.dump(site.callee_workflow))
end

# ---------------------------------------------------------------------------
# The call: ways the suite could exist and still not gate a caller.
# ---------------------------------------------------------------------------

assert_rejected("stop calling the Windows Go suite from test.yml") do |directory|
  mutate_windows_go_call(directory, "stop calling the suite from test.yml") { |site| site.jobs.delete(site.job_name) }
end

assert_rejected("stop calling the Windows Go suite from release.yml") do |directory|
  mutate_windows_go_call(directory, "stop calling the suite from release.yml", "release.yml") { |site| site.jobs.delete(site.job_name) }
end

assert_rejected("run the release call beside the build instead of before it") do |directory|
  mutate_windows_go_call(directory, "detach the release call from build", "release.yml") do |site|
    build = site.jobs.fetch("build")
    dependencies = Array(build.fetch("needs"))
    raise "fixture missing: release.yml build does not need #{site.job_name}" unless dependencies.include?(site.job_name)

    build["needs"] = dependencies - [site.job_name]
  end
end

assert_rejected("let release build run after a red Windows gate") do |directory|
  mutate_windows_go_call(directory, "if: always() on release build", "release.yml") { |site| site.jobs.fetch("build")["if"] = "${{ always() }}" }
end

assert_rejected("allow the release build on the publication path to fail") do |directory|
  mutate_windows_go_call(directory, "continue-on-error on release build", "release.yml") { |site| site.jobs.fetch("build")["continue-on-error"] = true }
end

assert_rejected("condition the release call with always()") do |directory|
  mutate_windows_go_call(directory, "if: always() on the release call", "release.yml") { |site| site.job["if"] = "${{ always() }}" }
end

assert_rejected("condition the test.yml call") do |directory|
  mutate_windows_go_call(directory, "if on the test.yml call") { |site| site.job["if"] = "${{ github.event_name == 'pull_request' }}" }
end

assert_rejected("allow the test.yml call to fail") do |directory|
  mutate_windows_go_call(directory, "continue-on-error on the test.yml call") { |site| site.job["continue-on-error"] = true }
end

assert_rejected("allow the release.yml call to fail") do |directory|
  mutate_windows_go_call(directory, "continue-on-error on the release call", "release.yml") { |site| site.job["continue-on-error"] = true }
end

# The point of the shared file, mutated: two workflows of the same shape are
# two descriptions, and only one of them is what a branch push ever runs.
assert_rejected("give test.yml and release.yml separate copies of the suite") do |directory|
  site = windows_go_call_site(directory, "test.yml", "separate copies of the suite")
  FileUtils.cp(site.callee_path, File.join(directory, "windows-go-copy.yml"))
  site.job["uses"] = "./.github/workflows/windows-go-copy.yml"
  File.write(site.caller_path, YAML.dump(site.caller_workflow))
end

assert_rejected("delete the reusable workflow both callers point at") do |directory|
  site = windows_go_call_site(directory, "test.yml", "delete the reusable workflow")
  FileUtils.rm(site.callee_path)
end

# ---------------------------------------------------------------------------
# The suite: ways it could be called and still not test what it claims to.
# Asserted once, because there is one file now -- weakening it weakens the
# branch push and the release together, which is exactly the property the move
# was for.
# ---------------------------------------------------------------------------

assert_rejected("make the suite unreachable by dropping workflow_call") do |directory|
  mutate_windows_go_call(directory, "drop workflow_call") do |site|
    workflow = site.callee_workflow
    raise "fixture missing: callee declares no trigger" unless workflow.key?("on") || workflow.key?(true)

    workflow.delete("on")
    workflow.delete(true)
    workflow["on"] = { "push" => { "branches" => ["main"] } }
  end
end

assert_rejected("move the Go suite off Windows") do |directory|
  mutate_windows_go_call(directory, "move the Go suite off Windows") { |site| site.suite_job["runs-on"] = "ubuntu-latest" }
end

assert_rejected("narrow the Windows Go suite to one package") do |directory|
  mutate_windows_go_call(directory, "narrow the Windows Go suite to one package") do |site|
    raise "fixture missing: Windows go test names no ./..." unless site.go_step.fetch("run").include?("./...")

    site.go_step["run"] = site.go_step.fetch("run").sub("./...", "./internal/harness")
  end
end

assert_rejected("narrow the Windows Go suite with -run") do |directory|
  mutate_windows_go_call(directory, "narrow the Windows Go suite with -run") do |site|
    site.go_step["run"] = site.go_step.fetch("run").sub(/\bgo\s+test\b/, "go test -run TestCmdline")
  end
end

assert_rejected("drop the Windows fixture build tag") do |directory|
  mutate_windows_go_call(directory, "drop the Windows fixture build tag") do |site|
    raise "fixture missing: Windows go test carries no -tags vctestfixture" unless site.go_step.fetch("run").include?("-tags vctestfixture")

    site.go_step["run"] = site.go_step.fetch("run").sub(/\s*-tags vctestfixture/, "")
  end
end

assert_rejected("allow the Windows Go job to fail") do |directory|
  mutate_windows_go_call(directory, "allow the Windows Go job to fail") { |site| site.suite_job["continue-on-error"] = true }
end

assert_rejected("allow the Windows go test step to fail") do |directory|
  mutate_windows_go_call(directory, "allow the Windows go test step to fail") { |site| site.go_step["continue-on-error"] = true }
end

assert_rejected("run the Windows Go job unconditionally with always()") do |directory|
  mutate_windows_go_call(directory, "if: always() on the Windows Go job") { |site| site.suite_job["if"] = "${{ always() }}" }
end

assert_rejected("skip the Windows go test step by condition") do |directory|
  mutate_windows_go_call(directory, "if on the Windows go test step") { |site| site.go_step["if"] = "${{ always() }}" }
end

["actions/checkout", "actions/setup-go"].each do |action|
  assert_rejected("unpin #{action} in the Windows Go suite") do |directory|
    mutate_windows_go_call(directory, "unpin #{action}") do |site|
      step = Array(site.suite_job["steps"]).find { |candidate| candidate["uses"].to_s.start_with?("#{action}@") }
      raise "fixture missing: Windows Go job does not use #{action}" unless step

      step["uses"] = "#{action}@v4"
    end
  end
end

assert_rejected("float the Windows Go toolchain off .go-version") do |directory|
  mutate_windows_go_call(directory, "float the Windows Go toolchain") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["uses"].to_s.start_with?("actions/setup-go@") }
    raise "fixture missing: Windows Go job does not use actions/setup-go" unless step
    raise "fixture missing: setup-go does not read .go-version" unless step.fetch("with", {})["go-version-file"] == ".go-version"

    step["with"] = step.fetch("with").merge("go-version-file" => nil, "go-version" => "1.21")
  end
end

# ---------------------------------------------------------------------------
# The native clipboard release gate: exact locked tools, exact unbundled Pi,
# and an explicit private clipboard owner. These mutations are intentionally on
# the shared Windows workflow, never on optional desktop packaging.
# ---------------------------------------------------------------------------

assert_rejected("remove pinned Node setup from the shared Windows gate") do |directory|
  mutate_windows_go_call(directory, "remove setup-node") do |site|
    steps = Array(site.suite_job["steps"])
    step = steps.find { |candidate| candidate["uses"].to_s.start_with?("actions/setup-node@") }
    raise "fixture missing: Windows gate does not use actions/setup-node" unless step

    steps.delete(step)
  end
end

assert_rejected("unpin actions/setup-node in the shared Windows gate") do |directory|
  mutate_windows_go_call(directory, "unpin setup-node") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["uses"].to_s.start_with?("actions/setup-node@") }
    raise "fixture missing: Windows gate does not use actions/setup-node" unless step

    step["uses"] = "actions/setup-node@v4"
  end
end

assert_rejected("float the native acceptance Node version") do |directory|
  mutate_windows_go_call(directory, "change pinned Node version") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["uses"].to_s.start_with?("actions/setup-node@") }
    raise "fixture missing: Windows gate does not use actions/setup-node" unless step
    raise "fixture missing: setup-node has no exact Node pin" unless step.fetch("with", {})["node-version"].to_s == DesktopCiContract::WINDOWS_NODE_VERSION

    step["with"]["node-version"] = "22"
  end
end

[
  [DesktopCiContract::DESKTOP_INSTALL, "npm install", "weaken locked desktop dependency install"],
  [DesktopCiContract::PI_INSTALL, "npm install --prefix runtime/pi", "weaken locked Pi dependency install"]
].each do |command, replacement, label|
  assert_rejected(label) do |directory|
    mutate_windows_go_call(directory, label) do |site|
      step = Array(site.suite_job["steps"]).find { |candidate| candidate["run"].to_s.strip == command }
      raise "fixture missing: Windows gate does not run #{command}" unless step

      step["run"] = replacement
    end
  end
end

assert_rejected("remove native clipboard acceptance from the shared Windows gate") do |directory|
  mutate_windows_go_call(directory, "remove native clipboard acceptance") do |site|
    steps = Array(site.suite_job["steps"])
    step = steps.find { |candidate| candidate["run"].to_s.strip == DesktopCiContract::NATIVE_ACCEPTANCE }
    raise "fixture missing: Windows gate does not run native clipboard acceptance" unless step

    steps.delete(step)
  end
end

assert_rejected("replace the native clipboard acceptance target with a fake-only test") do |directory|
  mutate_windows_go_call(directory, "change native clipboard acceptance target") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["run"].to_s.strip == DesktopCiContract::NATIVE_ACCEPTANCE }
    raise "fixture missing: Windows gate does not run native clipboard acceptance" unless step

    step["run"] = step.fetch("run").sub("pi-fullscreen-native-acceptance.test.ts", "pi-fullscreen-native-witness.test.ts")
  end
end

{
  "VC_ISOLATED_CLIPBOARD_ACCEPTANCE" => "remove explicit isolated clipboard ownership",
  "VC_NATIVE_PI_ENTRY" => "remove explicit unbundled Pi entry",
  "VC_NATIVE_PI_PACKAGE_DIR" => "remove explicit unbundled Pi package directory"
}.each do |variable, label|
  assert_rejected(label) do |directory|
    mutate_windows_go_call(directory, label) do |site|
      step = Array(site.suite_job["steps"]).find { |candidate| candidate["run"].to_s.strip == DesktopCiContract::NATIVE_ACCEPTANCE }
      raise "fixture missing: Windows gate does not run native clipboard acceptance" unless step
      environment = step.fetch("env")
      raise "fixture missing: native acceptance has no #{variable}" unless environment.key?(variable)

      environment.delete(variable)
    end
  end
end

assert_rejected("substitute the packaged bundle for unbundled real Pi") do |directory|
  mutate_windows_go_call(directory, "use staged bundle as native Pi entry") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["run"].to_s.strip == DesktopCiContract::NATIVE_ACCEPTANCE }
    raise "fixture missing: Windows gate does not run native clipboard acceptance" unless step

    step.fetch("env")["VC_NATIVE_PI_ENTRY"] = "${{ github.workspace }}/desktop/resources/staged/pi/agent/pi~BUN.mjs"
  end
end

assert_rejected("condition the native clipboard acceptance step") do |directory|
  mutate_windows_go_call(directory, "if on native clipboard acceptance") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["run"].to_s.strip == DesktopCiContract::NATIVE_ACCEPTANCE }
    raise "fixture missing: Windows gate does not run native clipboard acceptance" unless step

    step["if"] = "${{ runner.os == 'Windows' }}"
  end
end

assert_rejected("allow native clipboard acceptance to fail") do |directory|
  mutate_windows_go_call(directory, "continue-on-error on native clipboard acceptance") do |site|
    step = Array(site.suite_job["steps"]).find { |candidate| candidate["run"].to_s.strip == DesktopCiContract::NATIVE_ACCEPTANCE }
    raise "fixture missing: Windows gate does not run native clipboard acceptance" unless step

    step["continue-on-error"] = true
  end
end

# The permissions block, which is the one thing about a called workflow that
# fails OPEN when it is simply absent: no block means the caller's token, and
# release.yml's token can push and sign. Nothing about that is visible at the
# call site, so it is asserted at the file and mutated here.

assert_rejected("let the suite inherit the caller's token by dropping its permissions block") do |directory|
  mutate_windows_go_call(directory, "drop the callee permissions block") do |site|
    raise "fixture missing: callee declares no permissions block" unless site.callee_workflow.key?("permissions")

    site.callee_workflow.delete("permissions")
  end
end

assert_rejected("widen the suite's permissions to contents: write") do |directory|
  mutate_windows_go_call(directory, "contents: write on the callee") do |site|
    site.callee_workflow["permissions"] = { "contents" => "write" }
  end
end

["id-token", "attestations", "packages"].each do |scope|
  assert_rejected("grant the suite #{scope}: write on top of read-only contents") do |directory|
    mutate_windows_go_call(directory, "#{scope}: write on the callee") do |site|
      site.callee_workflow["permissions"] = { "contents" => "read", scope => "write" }
    end
  end
end

# One level down, where a read-only workflow block stops being the answer: a
# job may declare its own, and a job block wins.
assert_rejected("widen the suite job's permissions past the read-only workflow block") do |directory|
  mutate_windows_go_call(directory, "contents: write on the callee job") do |site|
    site.suite_job["permissions"] = { "contents" => "write" }
  end
end

assert_rejected("remove pinned runtime provision") do |directory|
  path = File.join(directory, "desktop-tests.yml")
  File.write(path, File.read(path).sub("        run: desktop/scripts/provision-pinned-pi-smoke.sh\n", ""))
end

assert_rejected("remove npm test") do |directory|
  path = File.join(directory, "desktop-tests.yml")
  File.write(path, File.read(path).sub("        run: npm test\n", ""))
end

assert_rejected("filter npm test") do |directory|
  path = File.join(directory, "desktop-tests.yml")
  File.write(path, File.read(path).sub("run: npm test", "run: npm test -- tests/contract.test.ts"))
end

assert_rejected("allow test failure") do |directory|
  path = File.join(directory, "desktop-tests.yml")
  File.write(path, File.read(path).sub("        run: npm test\n", "        continue-on-error: true\n        run: npm test\n"))
end

assert_rejected("bypass release packaging condition") do |directory|
  path = File.join(directory, "release.yml")
  File.write(path, File.read(path).sub("if: ${{ vars.DESKTOP_RELEASE == 'true' }}", "if: ${{ always() }}"))
end

assert_rejected("bypass desktop publication condition") do |directory|
  path = File.join(directory, "release.yml")
  workflow = File.read(path)
  block = workflow.index("  desktop-attach:")
  condition = workflow.index("if: ${{ vars.DESKTOP_RELEASE == 'true' }}", block)
  File.write(path, workflow[0...condition] + workflow[condition..].sub("if: ${{ vars.DESKTOP_RELEASE == 'true' }}", "if: ${{ always() }}"))
end

assert_rejected("detach CLI publication from qualified build") do |directory|
  path = File.join(directory, "release.yml")
  File.write(path, File.read(path).sub("  release:\n    name: Create GitHub Release\n    needs: build\n", "  release:\n    name: Create GitHub Release\n"))
end

assert_rejected("detach auth publication") do |directory|
  path = File.join(directory, "release.yml")
  File.write(path, File.read(path).sub("  publish-auth:\n    name: Sync release to void-auth\n    needs: release\n", "  publish-auth:\n    name: Sync release to void-auth\n"))
end

[
  ["test.yml", "desktop-mac-app"],
  ["test.yml", "desktop-windows-app"],
  ["release.yml", "build"],
  ["release.yml", "desktop-mac-app"],
  ["release.yml", "desktop-windows-app"]
].each do |file, job|
  assert_rejected("remove #{file} #{job} dependency") do |directory|
    path = File.join(directory, file)
    workflow = File.read(path, encoding: "UTF-8")
    block = workflow.index(/^  #{Regexp.escape(job)}:/)
    dependency = workflow.index(/^    needs: .*desktop-pinned-pi-smoke.*$/, block)
    raise "fixture dependency missing" unless dependency
    finish = workflow.index("\n", dependency)
    File.write(path, workflow[0...dependency] + workflow[(finish + 1)..])
  end
end

puts "desktop CI contract mutations rejected"
