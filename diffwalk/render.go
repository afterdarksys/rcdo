package diffwalk

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func RenderText(w io.Writer, diff Diff, summaryOnly bool) error {
	summary := diff.Summary()
	if _, err := fmt.Fprintf(w,
		"DIFF SUMMARY\nFiles: %d\nHunks: %d\nAdditions: %d\nDeletions: %d\nBinary files: %d\n",
		summary.Files, summary.Hunks, summary.Additions, summary.Deletions, summary.BinaryFiles,
	); err != nil {
		return err
	}
	if summaryOnly {
		return nil
	}

	for fileIndex, file := range diff.Files {
		if _, err := fmt.Fprintf(w,
			"\nFile %d of %d\nPath: %s\nChange: %s\nBinary: %t\nHunks: %d\n",
			fileIndex+1, len(diff.Files), file.Path, strings.ToUpper(string(file.Change)), file.Binary, len(file.Hunks),
		); err != nil {
			return err
		}
		if file.Change == Renamed {
			if _, err := fmt.Fprintf(w, "Old path: %s\nNew path: %s\n", file.OldPath, file.NewPath); err != nil {
				return err
			}
		}
		for hunkIndex, hunk := range file.Hunks {
			if _, err := fmt.Fprintf(w,
				"\nHunk %d of %d\nLocation: old line %d, new line %d\nSection: %s\n",
				hunkIndex+1, len(file.Hunks), hunk.OldStart, hunk.NewStart, emptyLabel(hunk.Section),
			); err != nil {
				return err
			}
			for _, line := range hunk.Lines {
				var err error
				switch line.Kind {
				case ContextLine:
					_, err = fmt.Fprintf(w, "Context old %d new %d: %s\n", line.OldNumber, line.NewNumber, line.Content)
				case RemovedLine:
					_, err = fmt.Fprintf(w, "Removed old %d: %s\n", line.OldNumber, line.Content)
				case AddedLine:
					_, err = fmt.Fprintf(w, "Added new %d: %s\n", line.NewNumber, line.Content)
				}
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func emptyLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return value
}

type jsonDiff struct {
	SchemaVersion string  `json:"schema_version"`
	Summary       Summary `json:"summary"`
	Files         []File  `json:"files"`
}

func RenderJSON(w io.Writer, diff Diff) error {
	files := diff.Files
	if files == nil {
		files = []File{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(jsonDiff{SchemaVersion: SchemaVersion, Summary: diff.Summary(), Files: files})
}
