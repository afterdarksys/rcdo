// textsee turns UTF-8 text from stdin or files into a word-frequency view.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
)

type Word struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

type Report struct {
	Words  int    `json:"words"`
	Unique int    `json:"unique"`
	Top    []Word `json:"top"`
}

func summarize(text string, limit int) Report {
	counts := map[string]int{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for _, word := range words {
		counts[word]++
	}
	ranked := make([]Word, 0, len(counts))
	for word, count := range counts {
		ranked = append(ranked, Word{word, count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Text < ranked[j].Text
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return Report{len(words), len(counts), ranked}
}

func run() error {
	top := flag.Int("top", 10, "number of words to display")
	jsonOutput := flag.Bool("json", false, "emit JSON for pipelines")
	literal := flag.String("text", "", "literal text to analyze (add '-' to also read stdin)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: textsee [-top N] [-json] [-text TEXT] [FILE ...]\nRead stdin when no input is given; '-' also means stdin.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *top < 1 {
		return fmt.Errorf("-top must be positive")
	}
	paths := flag.Args()
	hasLiteral := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "text" {
			hasLiteral = true
		}
	})
	if len(paths) == 0 && !hasLiteral {
		paths = []string{"-"}
	}
	var input strings.Builder
	if hasLiteral {
		input.WriteString(*literal)
		input.WriteByte('\n')
	}
	for _, path := range paths {
		var data []byte
		var err error
		if path == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return err
		}
		input.Write(data)
		input.WriteByte('\n')
	}
	report := summarize(input.String(), *top)
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	fmt.Printf("Words: %d  Unique: %d\n", report.Words, report.Unique)
	for _, word := range report.Top {
		width := int(30 * float64(word.Count) / float64(report.Top[0].Count))
		fmt.Printf("%6d  %-20s %s\n", word.Count, word.Text, strings.Repeat("#", width))
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "textsee:", err)
		os.Exit(1)
	}
}
