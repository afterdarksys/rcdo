package toolkit

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Color is a second channel. The action word and the class name are always
// printed. ANSI is added only for --color=always, or for --color=auto when
// stdout is a terminal, TERM is not dumb, and NO_COLOR is unset.

var colorSGR = map[string]string{
	"red": "31", "green": "32", "yellow": "33", "blue": "34", "magenta": "35", "cyan": "36", "white": "37",
	"bright-red": "91", "bright-green": "92", "bright-yellow": "93", "bright-blue": "94", "bright-magenta": "95", "bright-cyan": "96",
}

func defaultColorPalette() map[string]string {
	return map[string]string{
		"vm": "cyan", "container": "magenta", "route": "blue", "filter": "yellow", "lb": "bright-cyan",
		"logs": "white", "fs": "green", "dir": "bright-green", "proc": "bright-yellow", "devops": "white", "conn": "bright-blue",
		"action-create": "green", "action-update": "yellow", "action-delete": "red", "action-replace": "bright-red",
		"action-ensure": "cyan", "action-task": "blue", "action-noop": "white", "action-read": "white", "action-drift": "bright-magenta",
	}
}

func applyColorFlags(palette map[string]string, flags []string) error {
	for _, flag := range flags {
		class, color, ok := strings.Cut(flag, "=")
		if !ok || !operationLabel(class) || colorSGR[color] == "" {
			return fmt.Errorf("color flag must be class=color, with color one of red, green, yellow, blue, magenta, cyan, white, or the bright- forms")
		}
		palette[class] = color
	}
	return nil
}

func colorEnabled(mode string, w io.Writer) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default:
		if _, set := os.LookupEnv("NO_COLOR"); set || os.Getenv("TERM") == "dumb" {
			return false
		}
		file, ok := w.(*os.File)
		if !ok {
			return false
		}
		info, err := file.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
}

func paintToken(enabled bool, palette map[string]string, class, text string) string {
	if !enabled {
		return text
	}
	code := colorSGR[palette[class]]
	if code == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}
