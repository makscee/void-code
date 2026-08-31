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

  def assert(condition, message)
    raise message unless condition
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

    release_jobs = load_workflow(directory, "release.yml").fetch("jobs")
    assert(release_jobs.fetch("desktop-pinned-pi-smoke").fetch("uses") == "./.github/workflows/desktop-tests.yml", "release workflow must call the desktop test gate")
    assert(needs(release_jobs.fetch("build")).include?("desktop-pinned-pi-smoke"), "release CLI builds must depend on desktop tests")
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
