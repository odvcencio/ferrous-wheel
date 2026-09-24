package ops

import (
	"errors"
	"strings"
	"unicode"
)

// POSIXCommand quotes each argument for a remote POSIX shell. The result is a
// single command string. It supports spaces, quotes, newlines, and shell
// metacharacters as literal argument data. It does not specify PowerShell
// syntax or a transport for binary arguments.
func POSIXCommand(argv []string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", errors.New("ops: remote argv must start with a program")
	}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return "", errors.New("ops: remote argv contains NUL")
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " "), nil
}

// SSH returns a local process spec for a remote POSIX command. OpenSSH sends
// the quoted command through the remote user's shell. The host is always one
// local argv element, and -- ends SSH option parsing. SSH uses batch mode so
// automation cannot stop at an interactive password prompt.
func SSH(host string, remoteArgv []string) (Spec, error) {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsRune(host, 0) {
		return Spec{}, errors.New("ops: invalid SSH host")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return Spec{}, errors.New("ops: SSH host contains whitespace or control character")
		}
	}
	command, err := POSIXCommand(remoteArgv)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Argv: []string{"ssh", "-o", "BatchMode=yes", "--", host, command}}, nil
}

// Git runs git in a repository directory. It leaves the argument list intact.
func Git(dir string, args ...string) Spec {
	return Spec{Argv: append([]string{"git"}, args...), Cwd: dir}
}

// GH runs gh against one repository. An empty repo lets gh use its usual
// repository resolution. It leaves all other arguments intact.
func GH(repo string, args ...string) Spec {
	argv := []string{"gh"}
	if repo != "" {
		argv = append(argv, "-R", repo)
	}
	argv = append(argv, args...)
	return Spec{Argv: argv}
}
