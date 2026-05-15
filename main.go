// flamectl renders a pprof profile as a single-file interactive SVG
// flamegraph. It reads from a file, an HTTP URL (anything pprof serves
// over `/debug/pprof/...`), or stdin.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/pprof/profile"
)

func main() {
	var (
		out       = flag.String("o", "flame.svg", "output SVG path; - for stdout")
		idx       = flag.Int("sample", 0, "sample-value index to aggregate (default 0, usually inuse_objects, cpu samples, etc.)")
		focus     = flag.String("focus", "", "only include stacks containing this substring (case-insensitive)")
		title     = flag.String("title", "", "override the title shown on the chart")
		listOnly  = flag.Bool("list-samples", false, "print the available sample types in the profile and exit")
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, `flamectl renders pprof profiles as interactive SVG flamegraphs.

Usage:
  flamectl [flags] <profile-file-or-url>
  cat profile.pb.gz | flamectl [flags] -

Examples:
  flamectl cpu.pprof
  flamectl http://localhost:6060/debug/pprof/profile?seconds=10
  go tool pprof -proto -output /tmp/x.pb.gz -seconds 5 http://localhost:6060/debug/pprof/profile && flamectl /tmp/x.pb.gz

Flags:`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	prof, source, err := loadProfile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "flamectl:", err)
		os.Exit(1)
	}

	if *listOnly {
		for i, st := range prof.SampleType {
			marker := " "
			if i == *idx {
				marker = "*"
			}
			fmt.Printf("%s %d  %s [%s]\n", marker, i, st.Type, st.Unit)
		}
		return
	}

	root, unit, err := buildTree(prof, *idx, *focus)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flamectl: build tree:", err)
		os.Exit(1)
	}
	if root.value == 0 {
		fmt.Fprintln(os.Stderr, "flamectl: nothing to render after filtering")
		os.Exit(1)
	}

	t := *title
	if t == "" {
		t = source
		if *focus != "" {
			t += "  (focus: " + *focus + ")"
		}
	}

	opts := defaultOpts(t, unit)
	opts.subtitle = fmt.Sprintf("%s · %d frames · %s sample type",
		timeNanosT(prof.TimeNanos).toRFC(),
		countNodes(root),
		sampleTypeLabel(prof, *idx),
	)

	var w io.Writer
	if *out == "-" {
		w = os.Stdout
	} else {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "flamectl: create output:", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if err := renderSVG(w, root, opts); err != nil {
		fmt.Fprintln(os.Stderr, "flamectl: render:", err)
		os.Exit(1)
	}
	if *out != "-" {
		fmt.Fprintf(os.Stderr, "wrote %s\n", *out)
	}
}

// loadProfile reads a profile from a file path, an HTTP(S) URL, or stdin
// (when the arg is "-"). pprof's library handles gzip detection.
func loadProfile(src string) (*profile.Profile, string, error) {
	switch {
	case src == "-":
		p, err := profile.Parse(os.Stdin)
		return p, "stdin", err
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		req, err := http.NewRequest("GET", src, nil)
		if err != nil {
			return nil, src, err
		}
		req.Header.Set("Accept", "application/octet-stream")
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, src, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, src, fmt.Errorf("fetch %s: %s", src, resp.Status)
		}
		p, err := profile.Parse(resp.Body)
		return p, src, err
	default:
		f, err := os.Open(src)
		if err != nil {
			return nil, src, err
		}
		defer f.Close()
		p, err := profile.Parse(f)
		return p, src, err
	}
}

func countNodes(n *node) int {
	total := 1
	for _, c := range n.children {
		total += countNodes(c)
	}
	return total
}

func sampleTypeLabel(p *profile.Profile, idx int) string {
	if idx >= len(p.SampleType) {
		return "(unknown)"
	}
	st := p.SampleType[idx]
	return fmt.Sprintf("%s/%s", st.Type, st.Unit)
}

// timeNanos is a thin wrapper to format the profile timestamp without
// pulling extra deps.
type timeNanosT int64

func (t timeNanosT) toRFC() string {
	if t == 0 {
		return ""
	}
	return time.Unix(0, int64(t)).UTC().Format("2006-01-02 15:04 UTC")
}

