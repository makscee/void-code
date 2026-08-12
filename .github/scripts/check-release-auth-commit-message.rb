#!/usr/bin/env ruby
# frozen_string_literal: true

require "open3"
require "yaml"

workflow_path = File.expand_path("../workflows/release.yml", __dir__)
workflow = YAML.load_file(workflow_path)
step = workflow.fetch("jobs").fetch("publish-auth").fetch("steps").find do |candidate|
  candidate["name"] == "Commit and push to void-auth"
end
abort "auth-sync commit step not found" unless step

command = step.fetch("run")
match = command.match(/git commit -m "(.*?)"\n?\s*git push/m)
abort "auth-sync commit message not found" unless match

message = match[1].gsub("${{ github.ref_name }}", "v1.2.3")
abort "auth-sync commit message still contains deploy instructions" if message.match?(/ansible|ansible-playbook/i)

trailers, status = Open3.capture2("git", "interpret-trailers", "--parse", stdin_data: message)
abort "git does not recognize the co-author trailer" unless status.success? && trailers == "Co-Authored-By: github-actions[bot] <github-actions[bot]@users.noreply.github.com>\n"

puts "auth-sync commit message has a recognized co-author trailer and no Ansible instructions"
