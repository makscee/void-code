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
def mutate_windows_go_job(directory, label)
  path = File.join(directory, "test.yml")
  workflow = YAML.load_file(path)
  jobs = workflow.fetch("jobs")
  name, job = jobs.find { |_key, candidate| DesktopCiContract.windows_runner?(candidate) && DesktopCiContract.go_test_step(candidate) }
  raise "fixture missing: test.yml has no Windows job running go test (#{label})" unless job

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
