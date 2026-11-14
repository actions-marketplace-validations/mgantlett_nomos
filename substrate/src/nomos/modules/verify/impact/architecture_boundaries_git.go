// Package impact resolves Git-tracked polyglot file lists and TS/Svelte path aliases
// for architecture boundary discovery and enforcement (Law 4 & Law 7).
package impact

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func tryListGitTrackedPolyglotFiles(root string) ([]string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, false
	}
	wtRoot, wErr := workspace.FindWorkingTreeRoot(absRoot)
	if wErr != nil || wtRoot == "" {
		return nil, false
	}
	if filepath.Clean(wtRoot) != filepath.Clean(absRoot) &&
		workspace.SafeGitCommand(wtRoot, "check-ignore", "-q", absRoot).Run() == nil {
		return nil, false
	}
	raw, cErr := workspace.SafeGitCommand(absRoot, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if cErr != nil {
		return nil, false
	}
	return filterGitTrackedPolyglotPaths(absRoot, raw), true
}

func filterGitTrackedPolyglotPaths(absRoot string, raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte{0})
	seen := make(map[string]bool, len(parts))
	var out []string
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		slash := filepath.ToSlash(string(p))
		if strings.HasPrefix(slash, "..") || seen[slash] || hasSkippedBoundarySegment(slash) {
			continue
		}
		full := filepath.Join(absRoot, filepath.FromSlash(slash))
		if !vscope.IsSupportedPolyglotSourceFile(full) {
			continue
		}
		if fi, sErr := os.Stat(full); sErr == nil && !fi.IsDir() {
			seen[slash] = true
			out = append(out, slash)
		}
	}
	sort.Strings(out)
	return out
}

func hasSkippedBoundarySegment(relSlash string) bool {
	dir := filepath.ToSlash(filepath.Dir(relSlash))
	if dir == "." || dir == "" {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if strings.HasPrefix(seg, ".") || IsSkippedObligationDir(seg) {
			return true
		}
	}
	return false
}

func addAliasImportCandidates(relFile, rawImp string, add func(string)) {
	srcPrefix := enclosingSrcPrefix(relFile)
	switch {
	case strings.HasPrefix(rawImp, "$lib/"):
		rest := strings.TrimPrefix(rawImp, "$lib/")
		if srcPrefix != "" {
			add(path.Clean(path.Join(srcPrefix, "lib", rest)))
		}
		add(path.Clean(path.Join("src/lib", rest)))
		add(path.Clean(path.Join("lib", rest)))
	case strings.HasPrefix(rawImp, "@/"), strings.HasPrefix(rawImp, "~/"):
		rest := rawImp[2:]
		if srcPrefix != "" {
			add(path.Clean(path.Join(srcPrefix, rest)))
			if parent := path.Dir(srcPrefix); parent != "." && parent != "" {
				add(path.Clean(path.Join(parent, rest)))
			}
		}
		add(path.Clean(path.Join("src", rest)))
		add(path.Clean(rest))
	}
}

func enclosingSrcPrefix(relFile string) string {
	segs := strings.Split(filepath.ToSlash(filepath.Dir(relFile)), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i] == "src" {
			return strings.Join(segs[:i+1], "/")
		}
	}
	return ""
}
