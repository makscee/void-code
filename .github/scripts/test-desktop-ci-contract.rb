#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "tmpdir"
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

DesktopCiContract.check(SOURCE)

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
