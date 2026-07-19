package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/han/qrush/internal/protocol"
)

type Action int

const (
	ActionQueue Action = iota
	ActionList
	ActionKillServer
	ActionKillAll
	ActionKillJob
	ActionClearFinished
	ActionShowHelp
	ActionShowVersion
	ActionCatOutput
	ActionShowOutputFile
	ActionShowPID
	ActionRemoveJob
	ActionWaitJob
	ActionUrgent
	ActionGetState
	ActionSwapJobs
	ActionInfo
	ActionSetMaxSlots
	ActionGetMaxSlots
	ActionCountRunning
	ActionGetLabel
	ActionLastID
	ActionShowCmd
	ActionTail
	ActionGetEnv
	ActionSetEnv
	ActionUnsetEnv
	ActionGetLogdir
	ActionSetLogdir
	ActionForeground
	ActionServerMode
	ActionInteractive
	ActionSessionList
	ActionSessionCreate
	ActionSessionRename
	ActionSessionDelete
	ActionSessionMove
	ActionGroupList
	ActionGroupCreate
	ActionGroupRename
	ActionGroupDelete
	ActionTermList
	ActionTermKill
	ActionRerun
	ActionJobsView
	ActionConfigList
	ActionConfigGet
	ActionConfigSet
	ActionConfigEdit
	ActionConfigPath
	ActionGC
	ActionUpgrade
)

type Command struct {
	Action     Action
	Command    []string
	JobID      int
	JobID2     int
	Slots      int
	Label      string
	DependOn   []int
	ListFormat protocol.ListFormat
	EnvKey     string
	EnvValue   string
	LogdirPath string
	Logfile    string

	Session    string
	SessionArg string
	Message    string

	TimeoutMS int64
	Retries   int

	StoreOutput    bool
	SeparateStderr bool
	GzipOutput     bool
	RequireElevel  bool
	NonBlocking    bool
	NumSlots       int
}

func Parse(args []string) (*Command, error) {
	cmd := &Command{
		Action:      ActionList,
		JobID:       -1,
		JobID2:      -1,
		StoreOutput: true,
		NumSlots:    1,
		ListFormat:  protocol.FormatDefault,
	}

	// Bare `ru` opens the interactive management TUI. The plain job listing is
	// available via `ru -l`.
	if len(args) == 0 {
		cmd.Action = ActionInteractive
		return cmd, nil
	}

	if args[0] == "--server" {
		cmd.Action = ActionServerMode
		return cmd, nil
	}

	if args[0] == "session" {
		return parseSessionSubcommand(cmd, args[1:])
	}
	if args[0] == "group" {
		return parseGroupSubcommand(cmd, args[1:])
	}
	if args[0] == "term" {
		return parseTermSubcommand(cmd, args[1:])
	}
	if args[0] == "tui" {
		return parseTuiSubcommand(cmd, args[1:])
	}
	if args[0] == "config" {
		return parseConfigSubcommand(cmd, args[1:])
	}
	if args[0] == "gc" {
		cmd.Action = ActionGC
		return cmd, nil
	}
	if args[0] == "upgrade" {
		cmd.Action = ActionUpgrade
		return cmd, nil
	}

	i := 0
	actionSet := false

	for i < len(args) {
		arg := args[i]

		if strings.HasPrefix(arg, "--") {
			if err := parseLongFlag(cmd, args, &i, &actionSet); err != nil {
				return nil, err
			}
			continue
		}

		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			if err := parseShortFlags(cmd, args, &i, &actionSet); err != nil {
				return nil, err
			}
			continue
		}

		break
	}

	if i < len(args) && !actionSet {
		cmd.Action = ActionQueue
		cmd.Command = args[i:]
		return cmd, nil
	} else if i < len(args) && actionSet {
		cmd.Command = args[i:]
	}

	return cmd, nil
}

func parseShortFlags(cmd *Command, args []string, i *int, actionSet *bool) error {
	arg := args[*i]
	flags := arg[1:]

	for fi := 0; fi < len(flags); fi++ {
		flag := flags[fi]
		switch flag {
		case 'K':
			cmd.Action = ActionKillServer
			*actionSet = true
		case 'T':
			cmd.Action = ActionKillAll
			*actionSet = true
		case 'l':
			cmd.Action = ActionList
			*actionSet = true
		case 'h':
			cmd.Action = ActionShowHelp
			*actionSet = true
		case 'V':
			cmd.Action = ActionShowVersion
			*actionSet = true
		case 'C':
			cmd.Action = ActionClearFinished
			*actionSet = true
		case 'R':
			cmd.Action = ActionCountRunning
			*actionSet = true
		case 'q':
			cmd.Action = ActionLastID
			*actionSet = true
		case 'S':
			cmd.Action = ActionInteractive
			*actionSet = true
		case 'j':
			cmd.Action = ActionJobsView
			*actionSet = true
		case 'n':
			cmd.StoreOutput = false
		case 'E':
			cmd.SeparateStderr = true
		case 'z':
			cmd.GzipOutput = true
		case 'm':
			*i++
			if *i < len(args) {
				cmd.Message = args[*i]
			}
		case 'f':
			cmd.Action = ActionForeground
			*actionSet = true
		case 'B':
			cmd.NonBlocking = true
		case 'd':
			cmd.DependOn = append(cmd.DependOn, -1)
		case 'k':
			cmd.Action = ActionKillJob
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'c':
			cmd.Action = ActionCatOutput
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'o':
			cmd.Action = ActionShowOutputFile
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 't':
			cmd.Action = ActionTail
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'p':
			cmd.Action = ActionShowPID
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'i':
			cmd.Action = ActionInfo
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'r':
			cmd.Action = ActionRerun
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'x':
			cmd.Action = ActionRemoveJob
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'w':
			cmd.Action = ActionWaitJob
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'u':
			cmd.Action = ActionUrgent
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 's':
			cmd.Action = ActionGetState
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'a':
			cmd.Action = ActionGetLabel
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'F':
			cmd.Action = ActionShowCmd
			*actionSet = true
			cmd.JobID = optionalJobID(args, i)
		case 'g':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-g requires a session name")
			}
			cmd.Session = args[*i]
		case 'L':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-L requires a label")
			}
			cmd.Label = args[*i]
		case 'O':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-O requires a file path")
			}
			cmd.Logfile = args[*i]
		case 'N':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-N requires a slot count")
			}
			n, err := strconv.Atoi(args[*i])
			if err != nil {
				return fmt.Errorf("invalid slot count: %s", args[*i])
			}
			cmd.NumSlots = n
		case 'P':
			if *i+1 < len(args) {
				if n, err := strconv.Atoi(args[*i+1]); err == nil {
					cmd.Action = ActionSetMaxSlots
					cmd.Slots = n
					*actionSet = true
					*i++
				} else {
					cmd.Action = ActionGetMaxSlots
					*actionSet = true
				}
			} else {
				cmd.Action = ActionGetMaxSlots
				*actionSet = true
			}
		case 'D':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-D requires a dependency list")
			}
			ids, err := parseIDList(args[*i])
			if err != nil {
				return fmt.Errorf("invalid dependency list: %s", args[*i])
			}
			cmd.DependOn = append(cmd.DependOn, ids...)
		case 'W':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-W requires a dependency list")
			}
			ids, err := parseIDList(args[*i])
			if err != nil {
				return fmt.Errorf("invalid dependency list: %s", args[*i])
			}
			cmd.DependOn = append(cmd.DependOn, ids...)
			cmd.RequireElevel = true
		case 'U':
			cmd.Action = ActionSwapJobs
			*actionSet = true
			*i++
			if *i < len(args) {
				parts := strings.SplitN(args[*i], "-", 2)
				if len(parts) == 2 {
					id1, err1 := strconv.Atoi(parts[0])
					id2, err2 := strconv.Atoi(parts[1])
					if err1 == nil && err2 == nil {
						cmd.JobID = id1
						cmd.JobID2 = id2
					}
				}
			}
		case 'M':
			*i++
			if *i >= len(args) {
				return fmt.Errorf("-M requires a format")
			}
			switch args[*i] {
			case "json":
				cmd.ListFormat = protocol.FormatJSON
			case "tab":
				cmd.ListFormat = protocol.FormatTab
			default:
				cmd.ListFormat = protocol.FormatDefault
			}
			if !*actionSet {
				cmd.Action = ActionList
				*actionSet = true
			}
		default:
			return fmt.Errorf("unknown flag: -%c", flag)
		}

		// For flags that consume the next arg and break multi-flag parsing
		if flag == 'g' || flag == 'L' || flag == 'O' || flag == 'N' || flag == 'P' ||
			flag == 'D' || flag == 'W' || flag == 'U' || flag == 'M' || flag == 'm' {
			break
		}
	}

	*i++
	return nil
}

func parseLongFlag(cmd *Command, args []string, i *int, actionSet *bool) error {
	arg := args[*i]

	switch {
	case arg == "--getenv":
		cmd.Action = ActionGetEnv
		*actionSet = true
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--getenv requires a key")
		}
		cmd.EnvKey = args[*i]
	case arg == "--setenv":
		cmd.Action = ActionSetEnv
		*actionSet = true
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--setenv requires KEY=VALUE")
		}
		parts := strings.SplitN(args[*i], "=", 2)
		cmd.EnvKey = parts[0]
		if cmd.EnvKey == "" {
			return fmt.Errorf("--setenv requires a key")
		}
		if len(parts) == 2 {
			cmd.EnvValue = parts[1]
		}
	case arg == "--unsetenv":
		cmd.Action = ActionUnsetEnv
		*actionSet = true
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--unsetenv requires a key")
		}
		cmd.EnvKey = args[*i]
	case arg == "--get_logdir":
		cmd.Action = ActionGetLogdir
		*actionSet = true
	case arg == "--set_logdir":
		cmd.Action = ActionSetLogdir
		*actionSet = true
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--set_logdir requires a path")
		}
		cmd.LogdirPath = args[*i]
	case arg == "--timeout":
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--timeout requires a duration (e.g. 30m, 90s)")
		}
		d, err := time.ParseDuration(args[*i])
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid timeout %q (e.g. 30m, 90s)", args[*i])
		}
		cmd.TimeoutMS = d.Milliseconds()
	case arg == "--retries":
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--retries requires a count")
		}
		n, err := strconv.Atoi(args[*i])
		if err != nil || n < 0 {
			return fmt.Errorf("invalid retry count %q", args[*i])
		}
		cmd.Retries = n
	case arg == "--session":
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--session requires a name")
		}
		cmd.Session = args[*i]
	case arg == "--jobs":
		cmd.Action = ActionJobsView
		*actionSet = true
	case arg == "--interactive" || arg == "--tui":
		cmd.Action = ActionInteractive
		*actionSet = true
	case arg == "--slots":
		*actionSet = true
		if n, ok := optionalSlots(args, i); ok {
			cmd.Action = ActionSetMaxSlots
			cmd.Slots = n
		} else {
			cmd.Action = ActionGetMaxSlots
		}
	case arg == "--serialize":
		*i++
		if *i >= len(args) {
			return fmt.Errorf("--serialize requires a format")
		}
		switch args[*i] {
		case "json":
			cmd.ListFormat = protocol.FormatJSON
		case "tab":
			cmd.ListFormat = protocol.FormatTab
		default:
			cmd.ListFormat = protocol.FormatDefault
		}
		if !*actionSet {
			cmd.Action = ActionList
			*actionSet = true
		}
	default:
		return fmt.Errorf("unknown flag: %s", arg)
	}

	*i++
	return nil
}

func optionalJobID(args []string, i *int) int {
	if *i+1 < len(args) {
		if id, err := strconv.Atoi(args[*i+1]); err == nil {
			*i++
			return id
		}
	}
	return -1
}

func optionalSlots(args []string, i *int) (int, bool) {
	if *i+1 < len(args) {
		if n, err := strconv.Atoi(args[*i+1]); err == nil {
			*i++
			return n, true
		}
	}
	return 0, false
}

func parseSessionSubcommand(cmd *Command, args []string) (*Command, error) {
	if len(args) == 0 {
		cmd.Action = ActionSessionList
		return cmd, nil
	}

	switch args[0] {
	case "list":
		cmd.Action = ActionSessionList
	case "create":
		cmd.Action = ActionSessionCreate
		if len(args) < 2 {
			return nil, fmt.Errorf("session create requires a name")
		}
		cmd.Session = args[1]
	case "rename":
		cmd.Action = ActionSessionRename
		if len(args) < 3 {
			return nil, fmt.Errorf("session rename requires old and new names")
		}
		cmd.Session = args[1]
		cmd.SessionArg = args[2]
	case "delete":
		cmd.Action = ActionSessionDelete
		if len(args) < 2 {
			return nil, fmt.Errorf("session delete requires a name")
		}
		cmd.Session = args[1]
	case "move":
		cmd.Action = ActionSessionMove
		if len(args) < 3 {
			return nil, fmt.Errorf("session move requires a session and group")
		}
		cmd.Session = args[1]
		cmd.SessionArg = args[2]
	default:
		return nil, fmt.Errorf("unknown session command: %s", args[0])
	}
	return cmd, nil
}

// parseTuiSubcommand handles `ru tui` (interactive split view) and
// `ru tui -j` / `ru tui jobs` (open directly in the job-management view).
func parseTuiSubcommand(cmd *Command, args []string) (*Command, error) {
	cmd.Action = ActionInteractive
	if len(args) == 0 {
		return cmd, nil
	}
	switch args[0] {
	case "-j", "--jobs", "jobs":
		cmd.Action = ActionJobsView
	default:
		return nil, fmt.Errorf("unknown tui command: %s", args[0])
	}
	return cmd, nil
}

// parseConfigSubcommand handles `ru config [list|get|set|edit|path]`. The key
// lands in EnvKey and (for set) the value in EnvValue; a multi-word value
// (e.g. an on_finish command) is joined with spaces.
func parseConfigSubcommand(cmd *Command, args []string) (*Command, error) {
	if len(args) == 0 {
		cmd.Action = ActionConfigList
		return cmd, nil
	}
	switch args[0] {
	case "list":
		cmd.Action = ActionConfigList
	case "get":
		if len(args) < 2 {
			return nil, fmt.Errorf("config get requires a key")
		}
		cmd.Action = ActionConfigGet
		cmd.EnvKey = args[1]
	case "set":
		if len(args) < 3 {
			return nil, fmt.Errorf("config set requires a key and a value")
		}
		cmd.Action = ActionConfigSet
		cmd.EnvKey = args[1]
		cmd.EnvValue = strings.Join(args[2:], " ")
	case "edit":
		cmd.Action = ActionConfigEdit
	case "path":
		cmd.Action = ActionConfigPath
	default:
		return nil, fmt.Errorf("unknown config command: %s", args[0])
	}
	return cmd, nil
}

func parseTermSubcommand(cmd *Command, args []string) (*Command, error) {
	if len(args) == 0 {
		cmd.Action = ActionTermList
		return cmd, nil
	}
	switch args[0] {
	case "ls", "list":
		cmd.Action = ActionTermList
	case "kill":
		cmd.Action = ActionTermKill
		if len(args) < 3 {
			return nil, fmt.Errorf("term kill requires a session and pane")
		}
		cmd.Session = args[1]
		cmd.SessionArg = args[2]
	default:
		return nil, fmt.Errorf("unknown term command: %s", args[0])
	}
	return cmd, nil
}

func parseGroupSubcommand(cmd *Command, args []string) (*Command, error) {
	if len(args) == 0 {
		cmd.Action = ActionGroupList
		return cmd, nil
	}

	switch args[0] {
	case "list":
		cmd.Action = ActionGroupList
	case "create":
		cmd.Action = ActionGroupCreate
		if len(args) < 2 {
			return nil, fmt.Errorf("group create requires a name")
		}
		cmd.Session = args[1]
	case "rename":
		cmd.Action = ActionGroupRename
		if len(args) < 3 {
			return nil, fmt.Errorf("group rename requires old and new names")
		}
		cmd.Session = args[1]
		cmd.SessionArg = args[2]
	case "delete":
		cmd.Action = ActionGroupDelete
		if len(args) < 2 {
			return nil, fmt.Errorf("group delete requires a name")
		}
		cmd.Session = args[1]
	default:
		return nil, fmt.Errorf("unknown group command: %s", args[0])
	}
	return cmd, nil
}

func parseIDList(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	ids := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.Atoi(p)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
