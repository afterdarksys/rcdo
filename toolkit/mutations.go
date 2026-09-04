package toolkit

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func runPRChange(mode string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("pr-manager "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var title, body, base, head, repo string
	var number int
	var draft, execute bool
	fs.StringVar(&title, "title", "", "pull-request title")
	fs.StringVar(&body, "body", "", "pull-request body")
	fs.StringVar(&base, "base", "", "base branch")
	fs.StringVar(&head, "head", "", "head branch")
	fs.StringVar(&repo, "repo", "", "OWNER/REPO")
	fs.IntVar(&number, "number", 0, "pull-request number for update")
	fs.BoolVar(&draft, "draft", false, "create as draft")
	fs.BoolVar(&execute, "execute", false, "perform the GitHub mutation through gh")
	setAccessibleUsage(fs, "pr-manager "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	var ghArgs []string
	if mode == "create" {
		if title == "" || body == "" {
			return fmt.Errorf("create requires --title and --body")
		}
		ghArgs = []string{"pr", "create", "--title", title, "--body", body}
		if base != "" {
			ghArgs = append(ghArgs, "--base", base)
		}
		if head != "" {
			ghArgs = append(ghArgs, "--head", head)
		}
		if draft {
			ghArgs = append(ghArgs, "--draft")
		}
	} else {
		if number <= 0 || (title == "" && body == "") {
			return fmt.Errorf("update requires --number and at least one of --title or --body")
		}
		ghArgs = []string{"pr", "edit", strconv.Itoa(number)}
		if title != "" {
			ghArgs = append(ghArgs, "--title", title)
		}
		if body != "" {
			ghArgs = append(ghArgs, "--body", body)
		}
	}
	if repo != "" {
		ghArgs = append(ghArgs, "--repo", repo)
	}
	fmt.Fprintf(stdout, "OPERATION: pull request %s\n", mode)
	fmt.Fprintf(stdout, "REPOSITORY: %s\n", emptyValue(repo))
	if title != "" {
		fmt.Fprintf(stdout, "TITLE: %s\n", title)
	}
	if !execute {
		fmt.Fprintln(stdout, "EXECUTION: NOT RUN")
		fmt.Fprintln(stdout, "Review this preview, then add --execute to perform it through GitHub CLI.")
		return nil
	}
	command := exec.Command("gh", ghArgs...)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("gh %s failed: %w", mode, err)
	}
	return nil
}

func runGHA(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	mode := "check"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	switch mode {
	case "check", "dangerous":
		return runNativeRuleCheck("gha-tool "+mode, args, stdin, stdout, stderr, ghaRules, "actionlint", []string{"-no-color"})
	case "fmt", "fix":
		return runGHAFormat(mode, args, stdin, stdout, stderr)
	case "update":
		return runGHAUpdate(args, stdout, stderr)
	default:
		return fmt.Errorf("unknown gha-tool operation %q; expected check, dangerous, fmt, fix, or update", mode)
	}
}

func runGHAFormat(mode string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var input string
	var write bool
	fs := flag.NewFlagSet("gha-tool "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&input, "input", "-", "workflow file; use - for standard input")
	fs.BoolVar(&write, "write", false, "atomically replace the input file")
	setAccessibleUsage(fs, "gha-tool "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if write && input == "-" {
		return fmt.Errorf("--write requires --input with a file path")
	}
	data, err := readInput(input, stdin)
	if err != nil {
		return err
	}
	formatted := normalizeYAMLText(data)
	if mode == "fix" {
		for _, finding := range scanRules(formatted, basename(input), "continuous-integration", ghaRules).Findings {
			fmt.Fprintf(stderr, "unfixed %s: %s\n", finding.ID, finding.Title)
		}
	}
	if !write {
		_, err = stdout.Write(formatted)
		return err
	}
	return atomicReplace(input, formatted, stdout)
}

func runGHAUpdate(args []string, stdout, stderr io.Writer) error {
	var input, template string
	var write bool
	fs := flag.NewFlagSet("gha-tool update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&input, "input", "", "workflow file to update")
	fs.StringVar(&template, "template", "", "approved workflow template")
	fs.BoolVar(&write, "write", false, "atomically replace the input file")
	setAccessibleUsage(fs, "gha-tool update", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if input == "" || template == "" {
		return fmt.Errorf("update requires --input and --template")
	}
	data, err := os.ReadFile(template)
	if err != nil {
		return fmt.Errorf("read template: %w", err)
	}
	data = normalizeYAMLText(data)
	if !write {
		fmt.Fprintf(stdout, "UPDATE PREVIEW: %s from template %s\n", input, template)
		_, err = stdout.Write(data)
		return err
	}
	return atomicReplace(input, data, stdout)
}

func normalizeYAMLText(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	lines := strings.Split(string(data), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n")
}
