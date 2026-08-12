#!/usr/bin/env ruby
# frozen_string_literal: true

require "yaml"

WORKFLOW_DIR = File.expand_path("../workflows", __dir__)
STABLE_TAG = "v1.2.3"
CANARY_TAG = "v1.2.3-vi20.1"

def load_workflow(name)
  YAML.load_file(File.join(WORKFLOW_DIR, name))
end

def tag_patterns(workflow)
  trigger = workflow["on"] || workflow[true]
  trigger.fetch("push").fetch("tags")
end

def matches_tag?(patterns, tag)
  patterns.reduce(false) do |included, pattern|
    negated = pattern.start_with?("!")
    glob = negated ? pattern.delete_prefix("!") : pattern
    File.fnmatch?(glob, tag) ? !negated : included
  end
end

def assert(condition, message)
  abort message unless condition
end

release = load_workflow("release.yml")
canary = load_workflow("canary-release.yml")
release_patterns = tag_patterns(release)
canary_patterns = tag_patterns(canary)

assert(matches_tag?(release_patterns, STABLE_TAG), "stable tag must start the stable release workflow")
assert(!matches_tag?(canary_patterns, STABLE_TAG), "stable tag must not start the canary workflow")
assert(!matches_tag?(release_patterns, CANARY_TAG), "canary tag must not start the stable release workflow")
assert(matches_tag?(canary_patterns, CANARY_TAG), "canary tag must start the canary workflow")

release_jobs = release.fetch("jobs")
assert(release_jobs.fetch("release").fetch("needs") == "build", "stable release must depend on build")
assert(release_jobs.fetch("publish-auth").fetch("needs") == "release", "stable publish-auth must depend on release")
assert(canary.fetch("jobs").keys == ["build"], "canary graph must contain only its candidate build/release job")

puts "release tag graphs are exclusive: #{STABLE_TAG} => stable, #{CANARY_TAG} => canary"
