// Command doclinks checks the links in the repository's Markdown files:
// every relative link must point at an existing file, and every #anchor at a
// heading (as GitHub generates its id). Web links are not followed.
//
//	go run ./tools/doclinks [dir]
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	linkRE    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headingRE = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	schemeRE  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	root, _ = filepath.Abs(root)
	bad := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "dist" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		for _, p := range check(root, path) {
			rel, _ := filepath.Rel(root, path)
			fmt.Printf("%s: %s\n", rel, p)
			bad++
		}
		return nil
	})
	if bad > 0 {
		fmt.Printf("%d broken link(s)\n", bad)
		os.Exit(1)
	}
}

// check returns the broken links of one Markdown file.
func check(root, file string) []string {
	text, err := os.ReadFile(file)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, m := range linkRE.FindAllStringSubmatch(withoutCode(string(text)), -1) {
		link := m[1]
		if schemeRE.MatchString(link) {
			continue // web and mail links
		}
		target, anchor, _ := strings.Cut(link, "#")
		path := file
		if target != "" {
			path = filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
			if r, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(r, "..") {
				continue // a page of GitHub's around the repository, e.g. ../../releases
			}
			if _, err := os.Stat(path); err != nil {
				out = append(out, "missing "+link)
				continue
			}
		}
		if anchor != "" && strings.HasSuffix(path, ".md") && !anchors(path)[anchor] {
			out = append(out, "no heading for "+link)
		}
	}
	return out
}

// withoutCode drops fenced code blocks, whose brackets are not links.
func withoutCode(s string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if !in {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

var anchorCache = map[string]map[string]bool{}

// anchors lists the heading ids of a Markdown file as GitHub makes them.
func anchors(file string) map[string]bool {
	if a, ok := anchorCache[file]; ok {
		return a
	}
	a, seen := map[string]bool{}, map[string]int{}
	f, err := os.Open(file)
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		in := false
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				in = !in
			}
			if m := headingRE.FindStringSubmatch(line); m != nil && !in {
				s := slug(m[1])
				if n := seen[s]; n > 0 {
					a[fmt.Sprintf("%s-%d", s, n)] = true
				} else {
					a[s] = true
				}
				seen[s]++
			}
		}
	}
	anchorCache[file] = a
	return a
}

// slug is GitHub's heading id: lower case, punctuation dropped, spaces as -.
func slug(h string) string {
	h = strings.NewReplacer("`", "", "*", "").Replace(strings.ToLower(strings.TrimSpace(h)))
	var b strings.Builder
	for _, r := range h {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}
