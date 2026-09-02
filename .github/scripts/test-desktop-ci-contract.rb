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

# The mutations above key off the literal text of steps that already exist. The
# Windows Go job does not, so its text cannot be guessed here; these locate it
# the same way the contract does -- by structure -- and rewrite test.yml from
# the mutated tree. A mutation that cannot find its target raises rather than
# passing vacuously, which is the failure mode that would make this whole file
# decorative.
def mutate_windows_go_job(directory, label, file = "test.yml")
  path = File.join(directory, file)
  workflow = YAML.load_file(path)
  jobs = workflow.fetch("jobs")
  name, job = DesktopCiContract.windows_go_jobs(jobs).first
  raise "fixture missing: #{file} has no Windows job running go test (#{label})" unless job

  yield jobs, name, job, DesktopCiContract.go_test_step(job)
  File.write(path, YAML.dump(workflow))
end

DesktopCiContract.check(SOURCE)

assert_rejected("remove the Windows Go job") do |directory|
  mutate_windows_go_job(directory, "remove the Windows Go job") { |jobs, name, _job, _step| jobs.delete(name) }
end

assert_rejected("move the Go suite off Windows") do |directory|
  mutate_windows_go_job(directory, "move the Go suite off Windows") { |_jobs, _name, job, _step| job["runs-on"] = "ubuntu-latest" }
end

assert_rejected("narrow the Windows Go suite to one package") do |directory|
  mutate_windows_go_job(directory, "narrow the Windows Go suite to one package") do |_jobs, _name, _job, step|
    raise "fixture missing: Windows go test names no ./..." unless step.fetch("run").include?("./...")

    step["run"] = step.fetch("run").sub("./...", "./internal/harness")
  end
end

assert_rejected("narrow the Windows Go suite with -run") do |directory|
  mutate_windows_go_job(directory, "narrow the Windows Go suite with -run") do |_jobs, _name, _job, step|
    step["run"] = step.fetch("run").sub(/\bgo\s+test\b/, "go test -run TestCmdline")
  end
end

assert_rejected("allow the Windows Go job to fail") do |directory|
  mutate_windows_go_job(directory, "allow the Windows Go job to fail") { |_jobs, _name, job, _step| job["continue-on-error"] = true }
end

assert_rejected("allow the Windows go test step to fail") do |directory|
  mutate_windows_go_job(directory, "allow the Windows go test step to fail") { |_jobs, _name, _job, step| step["continue-on-error"] = true }
end

assert_rejected("drop the Windows fixture build tag") do |directory|
  mutate_windows_go_job(directory, "drop the Windows fixture build tag") do |_jobs, _name, _job, step|
    raise "fixture missing: Windows go test carries no -tags vctestfixture" unless step.fetch("run").include?("-tags vctestfixture")

    step["run"] = step.fetch("run").sub(/\s*-tags vctestfixture/, "")
  end
end

# The release-side Windows gate, mutated the same structural way and for the
# same reason -- its text is written by whoever implements it, not here. Each
# of these is a way the gate could be present and still not gate: absent,
# unreferenced by the job that builds, allowed to go red, skipped, narrowed to
# a package or a -run filter, compiled without the fixture tag the rest of the
# repository uses, or hung off a moving action tag. A release that survives any
# of them is a release the Windows suite did not qualify.
def mutate_release_windows_go_job(directory, label, &block)
  mutate_windows_go_job(directory, label, "release.yml", &block)
end

assert_rejected("remove the release Windows Go job") do |directory|
  mutate_release_windows_go_job(directory, "remove the release Windows Go job") { |jobs, name, _job, _step| jobs.delete(name) }
end

assert_rejected("run the release Windows Go job beside the build instead of before it") do |directory|
  mutate_release_windows_go_job(directory, "detach the release Windows Go job from build") do |jobs, name, _job, _step|
    build = jobs.fetch("build")
    dependencies = Array(build.fetch("needs"))
    raise "fixture missing: release.yml build does not need #{name}" unless dependencies.include?(name)

    build["needs"] = dependencies - [name]
  end
end

assert_rejected("move the release Go suite off Windows") do |directory|
  mutate_release_windows_go_job(directory, "move the release Go suite off Windows") { |_jobs, _name, job, _step| job["runs-on"] = "ubuntu-latest" }
end

assert_rejected("allow the release Windows Go job to fail") do |directory|
  mutate_release_windows_go_job(directory, "allow the release Windows Go job to fail") { |_jobs, _name, job, _step| job["continue-on-error"] = true }
end

assert_rejected("allow the release Windows go test step to fail") do |directory|
  mutate_release_windows_go_job(directory, "allow the release Windows go test step to fail") { |_jobs, _name, _job, step| step["continue-on-error"] = true }
end

assert_rejected("run the release Windows Go job unconditionally with always()") do |directory|
  mutate_release_windows_go_job(directory, "if: always() on the release Windows Go job") { |_jobs, _name, job, _step| job["if"] = "${{ always() }}" }
end

assert_rejected("skip the release Windows go test step by condition") do |directory|
  mutate_release_windows_go_job(directory, "if on the release Windows go test step") { |_jobs, _name, _job, step| step["if"] = "${{ always() }}" }
end

assert_rejected("narrow the release Windows Go suite to one package") do |directory|
  mutate_release_windows_go_job(directory, "narrow the release Windows Go suite to one package") do |_jobs, _name, _job, step|
    raise "fixture missing: release Windows go test names no ./..." unless step.fetch("run").include?("./...")

    step["run"] = step.fetch("run").sub("./...", "./internal/harness")
  end
end

assert_rejected("narrow the release Windows Go suite with -run") do |directory|
  mutate_release_windows_go_job(directory, "narrow the release Windows Go suite with -run") do |_jobs, _name, _job, step|
    step["run"] = step.fetch("run").sub(/\bgo\s+test\b/, "go test -run TestCmdline")
  end
end

assert_rejected("drop the release Windows fixture build tag") do |directory|
  mutate_release_windows_go_job(directory, "drop the release Windows fixture build tag") do |_jobs, _name, _job, step|
    raise "fixture missing: release Windows go test carries no -tags vctestfixture" unless step.fetch("run").include?("-tags vctestfixture")

    step["run"] = step.fetch("run").sub(/\s*-tags vctestfixture/, "")
  end
end

["actions/checkout", "actions/setup-go"].each do |action|
  assert_rejected("unpin #{action} in the release Windows Go job") do |directory|
    mutate_release_windows_go_job(directory, "unpin #{action}") do |_jobs, _name, job, _step|
      step = Array(job["steps"]).find { |candidate| candidate["uses"].to_s.start_with?("#{action}@") }
      raise "fixture missing: release Windows job does not use #{action}" unless step

      step["uses"] = "#{action}@v4"
    end
  end
end

assert_rejected("float the release Windows Go toolchain off .go-version") do |directory|
  mutate_release_windows_go_job(directory, "float the release Windows Go toolchain") do |_jobs, _name, job, _step|
    step = Array(job["steps"]).find { |candidate| candidate["uses"].to_s.start_with?("actions/setup-go@") }
    raise "fixture missing: release Windows job does not use actions/setup-go" unless step
    raise "fixture missing: release setup-go does not read .go-version" unless step.fetch("with", {})["go-version-file"] == ".go-version"

    step["with"] = step.fetch("with").merge("go-version-file" => nil, "go-version" => "1.21")
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
