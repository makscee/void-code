//go:build !vctestfixture

package main

// Normal builds install the managed web-search dependencies with the real npm.
var installManagedWebSearchDependencies = npmManagedWebSearchInstall
