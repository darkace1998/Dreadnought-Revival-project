//go:build windows

package server

import (
	"os/exec"
	"strings"
	"syscall"
)

// applyRawCmdLine takes over construction of the child's command line.
//
// Why this is necessary, and not a stylistic preference:
//
// Go builds a Windows command line with syscall.EscapeArg, which quotes an
// argument containing spaces as a WHOLE token:
//
//	"-ABSLOG=G:\...\Dedicated server\logs\x.log"
//
// That is correct for any program that reads its arguments through
// CommandLineToArgvW, because the unquoting happens before the program sees
// them. Unreal does not do that. FCommandLine on Windows takes the raw
// GetCommandLineW string, and FParse::Value("ABSLOG=") reads the value in place
// starting immediately after the '=' -- stopping at the first whitespace unless
// the value ITSELF begins with a quote. Given the token above, the leading quote
// sits before "-ABSLOG", not before the value, so the engine reads
//
//	G:\...\Dedicated
//
// and writes its log to a path that does not exist, silently falling back to the
// shared default. The value has to be quoted after the '=':
//
//	-ABSLOG="G:\...\Dedicated server\logs\x.log"
//
// which is a raw command line Go's escaping will not produce, hence CmdLine.
//
// This only applies to the direct-exec path. Under Wine the argv goes through
// Wine's own CreateProcess emulation, which is why the Revival project never hit
// it.
func applyRawCmdLine(cmd *exec.Cmd, exe string, args []string) {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWholeToken(exe))
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = strings.Join(parts, " ")
}

// quoteArg quotes an argument for Unreal's parser: for a "-Key=Value" switch the
// quotes go around Value only; anything else is quoted as a whole token.
func quoteArg(a string) string {
	if !strings.ContainsAny(a, " \t") {
		return a
	}
	if strings.HasPrefix(a, "-") {
		if i := strings.Index(a, "="); i > 0 {
			return a[:i+1] + quoteWholeToken(a[i+1:])
		}
	}
	return quoteWholeToken(a)
}

// quoteWholeToken wraps s in quotes, escaping any trailing backslashes so they
// cannot escape the closing quote. A path like `C:\dir\` would otherwise produce
// `"C:\dir\"`, where the final backslash escapes the quote and swallows the rest
// of the command line.
func quoteWholeToken(s string) string {
	trailing := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		trailing++
	}
	return `"` + s + strings.Repeat(`\`, trailing) + `"`
}
