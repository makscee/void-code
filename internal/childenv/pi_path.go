package childenv

import "strings"

// PiPath is the PATH VC composes for Pi: the directory of the Node it bundled,
// then the smallest set of system directories a child process needs, and nothing
// the parent had. It is the CLI half of the rule desktopChildEnv already applies
// in desktop/src/main/desktop-child-env.ts, and the two must not answer
// differently — the incident this closes was the CLI handing Pi os.Environ()
// whole, so `#!/usr/bin/env node` picked whichever node the user's PATH named
// first.
//
// platform is a parameter, never runtime.GOOS read here, so the Windows shape is
// checkable from the ubuntu runner that gates a push. parent is the whole
// environment rather than a SystemRoot string because Windows spells variable
// names in whatever case the parent used, and the lookup below has to survive
// that the way its neighbour in the desktop does.
func PiPath(platform string, parent []string, privateNode string) string {
	if platform == "windows" {
		systemRoot := lookup(parent, "SystemRoot")
		if strings.TrimSpace(systemRoot) == "" {
			systemRoot = `C:\Windows`
		}
		return join(";", windowsDirectoryOf(privateNode), windowsJoin(systemRoot, "System32"))
	}
	return join(":", unixDirectoryOf(privateNode), "/usr/bin", "/bin")
}

// lookup reads a variable out of a raw environment, matching the name without
// regard to case. Windows writes SystemRoot, SYSTEMROOT and Path as it likes;
// a case-sensitive lookup finds nothing on the machines that chose another
// spelling and is right on every machine anyone tests it on.
func lookup(parent []string, name string) string {
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// unixDirectoryOf and windowsDirectoryOf name the directory holding an
// executable, and return "" when the argument names no directory at all. The
// empty answer is the point rather than an oversight: "" and "." on a PATH both
// mean the working directory, so a caller with no bundled Node to name must end
// up with a PATH that has no entry for it, not with an entry that resolves next
// to whatever project the user happens to be sitting in. Pi failing to start is
// the correct outcome there.
func unixDirectoryOf(executable string) string {
	trimmed := strings.TrimSpace(executable)
	index := strings.LastIndex(trimmed, "/")
	if index < 0 {
		return ""
	}
	if index == 0 {
		return "/"
	}
	return trimmed[:index]
}

// The Windows half cannot use path/filepath: the suite runs on unix, where
// filepath knows only "/" and would answer "." for every backslash path it is
// given. Both separators are accepted because Windows itself accepts both.
func windowsDirectoryOf(executable string) string {
	trimmed := strings.TrimSpace(executable)
	index := strings.LastIndexAny(trimmed, `\/`)
	if index < 0 {
		return ""
	}
	directory := trimmed[:index]
	switch {
	case directory == "":
		return `\`
	// `C:` alone is not the root of C: it is the working directory recorded for
	// that drive, which is the relative entry this whole function refuses.
	case len(directory) == 2 && directory[1] == ':':
		return directory + `\`
	}
	return directory
}

func windowsJoin(directory, name string) string {
	return strings.TrimRight(directory, `\/`) + `\` + name
}

func join(separator string, entries ...string) string {
	present := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry != "" {
			present = append(present, entry)
		}
	}
	return strings.Join(present, separator)
}
