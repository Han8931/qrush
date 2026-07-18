package cli

import (
	"testing"

	"github.com/han/qrush/internal/protocol"
)

func TestParseNoArgs(t *testing.T) {
	// Bare `ru` opens the interactive management TUI; `ru -l` prints the list.
	cmd, err := Parse([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionInteractive {
		t.Errorf("expected ActionInteractive, got %d", cmd.Action)
	}
}

func TestParseCommand(t *testing.T) {
	cmd, err := Parse([]string{"echo", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionQueue {
		t.Errorf("expected ActionQueue, got %d", cmd.Action)
	}
	if len(cmd.Command) != 2 || cmd.Command[0] != "echo" || cmd.Command[1] != "hello" {
		t.Errorf("unexpected command: %v", cmd.Command)
	}
}

func TestParseActionFlags(t *testing.T) {
	tests := []struct {
		args   []string
		action Action
	}{
		{[]string{"-K"}, ActionKillServer},
		{[]string{"-T"}, ActionKillAll},
		{[]string{"-l"}, ActionList},
		{[]string{"-h"}, ActionShowHelp},
		{[]string{"-V"}, ActionShowVersion},
		{[]string{"-C"}, ActionClearFinished},
		{[]string{"-R"}, ActionCountRunning},
		{[]string{"-q"}, ActionLastID},
	}

	for _, tt := range tests {
		cmd, err := Parse(tt.args)
		if err != nil {
			t.Errorf("Parse(%v): %v", tt.args, err)
			continue
		}
		if cmd.Action != tt.action {
			t.Errorf("Parse(%v): expected action %d, got %d", tt.args, tt.action, cmd.Action)
		}
	}
}

func TestParseJobIDFlags(t *testing.T) {
	tests := []struct {
		args   []string
		action Action
		jobID  int
	}{
		{[]string{"-c", "5"}, ActionCatOutput, 5},
		{[]string{"-t", "3"}, ActionTail, 3},
		{[]string{"-o", "7"}, ActionShowOutputFile, 7},
		{[]string{"-p", "1"}, ActionShowPID, 1},
		{[]string{"-i", "2"}, ActionInfo, 2},
		{[]string{"-r", "4"}, ActionRerun, 4},
		{[]string{"-x", "4"}, ActionRemoveJob, 4},
		{[]string{"-w", "6"}, ActionWaitJob, 6},
		{[]string{"-k", "8"}, ActionKillJob, 8},
		{[]string{"-u", "9"}, ActionUrgent, 9},
		{[]string{"-s", "0"}, ActionGetState, 0},
		{[]string{"-a", "10"}, ActionGetLabel, 10},
		{[]string{"-F", "11"}, ActionShowCmd, 11},
	}

	for _, tt := range tests {
		cmd, err := Parse(tt.args)
		if err != nil {
			t.Errorf("Parse(%v): %v", tt.args, err)
			continue
		}
		if cmd.Action != tt.action {
			t.Errorf("Parse(%v): expected action %d, got %d", tt.args, tt.action, cmd.Action)
		}
		if cmd.JobID != tt.jobID {
			t.Errorf("Parse(%v): expected jobID %d, got %d", tt.args, tt.jobID, cmd.JobID)
		}
	}
}

func TestParseJobIDOptional(t *testing.T) {
	cmd, err := Parse([]string{"-c"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionCatOutput {
		t.Errorf("expected ActionCatOutput, got %d", cmd.Action)
	}
	if cmd.JobID != -1 {
		t.Errorf("expected jobID -1 (last), got %d", cmd.JobID)
	}
}

func TestParseModifiers(t *testing.T) {
	cmd, err := Parse([]string{"-nEz", "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.StoreOutput != false {
		t.Error("expected StoreOutput=false")
	}
	if cmd.SeparateStderr != true {
		t.Error("expected SeparateStderr=true")
	}
	if cmd.GzipOutput != true {
		t.Error("expected GzipOutput=true")
	}
}

func TestParseMessage(t *testing.T) {
	cmd, err := Parse([]string{"-m", "fixing build", "make"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Message != "fixing build" {
		t.Errorf("expected message 'fixing build', got %q", cmd.Message)
	}
	if cmd.Action != ActionQueue {
		t.Errorf("expected ActionQueue, got %d", cmd.Action)
	}
}

func TestParseForeground(t *testing.T) {
	cmd, err := Parse([]string{"-f", "sleep", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionForeground {
		t.Errorf("expected ActionForeground, got %d", cmd.Action)
	}
	if len(cmd.Command) != 2 || cmd.Command[0] != "sleep" {
		t.Errorf("unexpected command: %v", cmd.Command)
	}
}

func TestParseLabel(t *testing.T) {
	cmd, err := Parse([]string{"-L", "build", "make"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Label != "build" {
		t.Errorf("expected label 'build', got %q", cmd.Label)
	}
}

func TestParseNumSlots(t *testing.T) {
	cmd, err := Parse([]string{"-N", "4", "make"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.NumSlots != 4 {
		t.Errorf("expected NumSlots=4, got %d", cmd.NumSlots)
	}
}

func TestParseDependency(t *testing.T) {
	cmd, err := Parse([]string{"-D", "1,2,3", "echo", "done"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.DependOn) != 3 || cmd.DependOn[0] != 1 || cmd.DependOn[1] != 2 || cmd.DependOn[2] != 3 {
		t.Errorf("unexpected DependOn: %v", cmd.DependOn)
	}
}

func TestParseDependLast(t *testing.T) {
	cmd, err := Parse([]string{"-d", "echo", "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.DependOn) != 1 || cmd.DependOn[0] != -1 {
		t.Errorf("expected DependOn=[-1], got %v", cmd.DependOn)
	}
}

func TestParseRequireElevel(t *testing.T) {
	cmd, err := Parse([]string{"-W", "0", "echo", "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.RequireElevel {
		t.Error("expected RequireElevel=true")
	}
	if len(cmd.DependOn) != 1 || cmd.DependOn[0] != 0 {
		t.Errorf("expected DependOn=[0], got %v", cmd.DependOn)
	}
}

func TestParseSetMaxSlots(t *testing.T) {
	cmd, err := Parse([]string{"-P", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSetMaxSlots {
		t.Errorf("expected ActionSetMaxSlots, got %d", cmd.Action)
	}
	if cmd.Slots != 3 {
		t.Errorf("expected Slots=3, got %d", cmd.Slots)
	}
}

func TestParseGetMaxSlots(t *testing.T) {
	cmd, err := Parse([]string{"-P"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionGetMaxSlots {
		t.Errorf("expected ActionGetMaxSlots, got %d", cmd.Action)
	}
}

func TestParseSetMaxSlotsLong(t *testing.T) {
	cmd, err := Parse([]string{"--slots", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSetMaxSlots {
		t.Errorf("expected ActionSetMaxSlots, got %d", cmd.Action)
	}
	if cmd.Slots != 3 {
		t.Errorf("expected Slots=3, got %d", cmd.Slots)
	}
}

func TestParseGetMaxSlotsLong(t *testing.T) {
	cmd, err := Parse([]string{"--slots"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionGetMaxSlots {
		t.Errorf("expected ActionGetMaxSlots, got %d", cmd.Action)
	}
}

func TestParseSwap(t *testing.T) {
	cmd, err := Parse([]string{"-U", "1-3"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSwapJobs {
		t.Errorf("expected ActionSwapJobs, got %d", cmd.Action)
	}
	if cmd.JobID != 1 || cmd.JobID2 != 3 {
		t.Errorf("expected swap 1-3, got %d-%d", cmd.JobID, cmd.JobID2)
	}
}

func TestParseSerializeFormat(t *testing.T) {
	tests := []struct {
		args   []string
		format protocol.ListFormat
	}{
		{[]string{"-M", "json"}, protocol.FormatJSON},
		{[]string{"-M", "tab"}, protocol.FormatTab},
		{[]string{"--serialize", "json"}, protocol.FormatJSON},
	}

	for _, tt := range tests {
		cmd, err := Parse(tt.args)
		if err != nil {
			t.Errorf("Parse(%v): %v", tt.args, err)
			continue
		}
		if cmd.ListFormat != tt.format {
			t.Errorf("Parse(%v): expected format %d, got %d", tt.args, tt.format, cmd.ListFormat)
		}
	}
}

func TestParseLongFlags(t *testing.T) {
	tests := []struct {
		args   []string
		action Action
	}{
		{[]string{"--getenv", "FOO"}, ActionGetEnv},
		{[]string{"--setenv", "FOO=bar"}, ActionSetEnv},
		{[]string{"--unsetenv", "FOO"}, ActionUnsetEnv},
		{[]string{"--get_logdir"}, ActionGetLogdir},
		{[]string{"--set_logdir", "/tmp"}, ActionSetLogdir},
	}

	for _, tt := range tests {
		cmd, err := Parse(tt.args)
		if err != nil {
			t.Errorf("Parse(%v): %v", tt.args, err)
			continue
		}
		if cmd.Action != tt.action {
			t.Errorf("Parse(%v): expected action %d, got %d", tt.args, tt.action, cmd.Action)
		}
	}
}

func TestParseSetEnvKeyValue(t *testing.T) {
	cmd, err := Parse([]string{"--setenv", "FOO=bar"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.EnvKey != "FOO" || cmd.EnvValue != "bar" {
		t.Errorf("expected FOO=bar, got %s=%s", cmd.EnvKey, cmd.EnvValue)
	}
}

func TestParseServerMode(t *testing.T) {
	cmd, err := Parse([]string{"--server"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionServerMode {
		t.Errorf("expected ActionServerMode, got %d", cmd.Action)
	}
}

func TestParseUnknownFlag(t *testing.T) {
	_, err := Parse([]string{"-X"})
	if err == nil {
		t.Error("expected error for unknown flag")
	}
}

func TestParseUnknownLongFlag(t *testing.T) {
	_, err := Parse([]string{"--bogus"})
	if err == nil {
		t.Error("expected error for unknown long flag")
	}
}

func TestParseLogfile(t *testing.T) {
	cmd, err := Parse([]string{"-O", "/tmp/out.log", "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Logfile != "/tmp/out.log" {
		t.Errorf("expected logfile '/tmp/out.log', got %q", cmd.Logfile)
	}
}

func TestParseIDList(t *testing.T) {
	ids, err := parseIDList("1,2,3")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("unexpected ids: %v", ids)
	}
}

func TestParseIDListInvalid(t *testing.T) {
	_, err := parseIDList("1,abc")
	if err == nil {
		t.Error("expected error for invalid id list")
	}
}

func TestParseInvalidSlotCount(t *testing.T) {
	_, err := Parse([]string{"-N", "abc"})
	if err == nil {
		t.Error("expected error for invalid slot count")
	}
}

func TestParseMissingFlagOperands(t *testing.T) {
	tests := [][]string{
		{"-L"},
		{"-O"},
		{"-N"},
		{"-D"},
		{"-W"},
		{"-M"},
		{"--getenv"},
		{"--setenv"},
		{"--unsetenv"},
		{"--set_logdir"},
		{"--session"},
		{"--serialize"},
	}

	for _, args := range tests {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v): expected missing operand error", args)
		}
	}
}

func TestParseInteractive(t *testing.T) {
	cmd, err := Parse([]string{"-S"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionInteractive {
		t.Errorf("expected ActionInteractive, got %d", cmd.Action)
	}
}

func TestParseSessionFlag(t *testing.T) {
	cmd, err := Parse([]string{"-g", "build", "make"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Session != "build" {
		t.Errorf("expected session 'build', got %q", cmd.Session)
	}
	if cmd.Action != ActionQueue {
		t.Errorf("expected ActionQueue, got %d", cmd.Action)
	}
}

func TestParseLongSessionFlag(t *testing.T) {
	cmd, err := Parse([]string{"--session", "deploy", "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Session != "deploy" {
		t.Errorf("expected session 'deploy', got %q", cmd.Session)
	}
}

func TestParseTuiSubcommand(t *testing.T) {
	cmd, err := Parse([]string{"tui"})
	if err != nil || cmd.Action != ActionInteractive {
		t.Fatalf("`tui` => action=%d err=%v, want ActionInteractive", cmd.Action, err)
	}
	for _, args := range [][]string{{"tui", "-j"}, {"tui", "--jobs"}, {"tui", "jobs"}} {
		cmd, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if cmd.Action != ActionJobsView {
			t.Errorf("Parse(%v): expected ActionJobsView, got %d", args, cmd.Action)
		}
	}
	if _, err := Parse([]string{"tui", "bogus"}); err == nil {
		t.Error("expected error for unknown tui command")
	}
}

func TestParseJobsView(t *testing.T) {
	for _, args := range [][]string{{"-j"}, {"--jobs"}} {
		cmd, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if cmd.Action != ActionJobsView {
			t.Errorf("Parse(%v): expected ActionJobsView, got %d", args, cmd.Action)
		}
	}
}

func TestParseRerunAndRemove(t *testing.T) {
	cmd, err := Parse([]string{"-r", "5"})
	if err != nil || cmd.Action != ActionRerun || cmd.JobID != 5 {
		t.Fatalf("-r 5 => action=%d id=%d err=%v", cmd.Action, cmd.JobID, err)
	}
	cmd, err = Parse([]string{"-x", "5"})
	if err != nil || cmd.Action != ActionRemoveJob || cmd.JobID != 5 {
		t.Fatalf("-x 5 => action=%d id=%d err=%v", cmd.Action, cmd.JobID, err)
	}
}

func TestParseTermList(t *testing.T) {
	for _, args := range [][]string{{"term"}, {"term", "ls"}, {"term", "list"}} {
		cmd, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if cmd.Action != ActionTermList {
			t.Errorf("Parse(%v): expected ActionTermList, got %d", args, cmd.Action)
		}
	}
}

func TestParseTermKill(t *testing.T) {
	cmd, err := Parse([]string{"term", "kill", "work", "p3"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionTermKill {
		t.Errorf("expected ActionTermKill, got %d", cmd.Action)
	}
	if cmd.Session != "work" || cmd.SessionArg != "p3" {
		t.Errorf("got session=%q pane=%q, want work/p3", cmd.Session, cmd.SessionArg)
	}
	if _, err := Parse([]string{"term", "kill", "work"}); err == nil {
		t.Error("expected error when pane is missing")
	}
}

func TestParseSessionList(t *testing.T) {
	cmd, err := Parse([]string{"session", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSessionList {
		t.Errorf("expected ActionSessionList, got %d", cmd.Action)
	}
}

func TestParseSessionCreate(t *testing.T) {
	cmd, err := Parse([]string{"session", "create", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSessionCreate {
		t.Errorf("expected ActionSessionCreate, got %d", cmd.Action)
	}
	if cmd.Session != "build" {
		t.Errorf("expected session 'build', got %q", cmd.Session)
	}
}

func TestParseSessionRename(t *testing.T) {
	cmd, err := Parse([]string{"session", "rename", "old", "new"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSessionRename {
		t.Errorf("expected ActionSessionRename, got %d", cmd.Action)
	}
	if cmd.Session != "old" || cmd.SessionArg != "new" {
		t.Errorf("expected old->new, got %q->%q", cmd.Session, cmd.SessionArg)
	}
}

func TestParseSessionDelete(t *testing.T) {
	cmd, err := Parse([]string{"session", "delete", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSessionDelete {
		t.Errorf("expected ActionSessionDelete, got %d", cmd.Action)
	}
	if cmd.Session != "build" {
		t.Errorf("expected session 'build', got %q", cmd.Session)
	}
}

func TestParseSessionMove(t *testing.T) {
	cmd, err := Parse([]string{"session", "move", "build", "work"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSessionMove {
		t.Errorf("expected ActionSessionMove, got %d", cmd.Action)
	}
	if cmd.Session != "build" || cmd.SessionArg != "work" {
		t.Errorf("expected build->work, got %q->%q", cmd.Session, cmd.SessionArg)
	}
}

func TestParseGroupCommands(t *testing.T) {
	tests := []struct {
		args   []string
		action Action
	}{
		{[]string{"group"}, ActionGroupList},
		{[]string{"group", "list"}, ActionGroupList},
		{[]string{"group", "create", "work"}, ActionGroupCreate},
		{[]string{"group", "rename", "work", "ci"}, ActionGroupRename},
		{[]string{"group", "delete", "ci"}, ActionGroupDelete},
	}

	for _, tt := range tests {
		cmd, err := Parse(tt.args)
		if err != nil {
			t.Errorf("Parse(%v): %v", tt.args, err)
			continue
		}
		if cmd.Action != tt.action {
			t.Errorf("Parse(%v): expected action %d, got %d", tt.args, tt.action, cmd.Action)
		}
	}
}

func TestParseSessionNoArgs(t *testing.T) {
	cmd, err := Parse([]string{"session"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSessionList {
		t.Errorf("expected ActionSessionList for bare 'session', got %d", cmd.Action)
	}
}

func TestParseSessionCreateNoName(t *testing.T) {
	_, err := Parse([]string{"session", "create"})
	if err == nil {
		t.Error("expected error for session create without name")
	}
}

func TestParseSessionUnknown(t *testing.T) {
	_, err := Parse([]string{"session", "bogus"})
	if err == nil {
		t.Error("expected error for unknown session subcommand")
	}
}

func TestParseConfigSubcommands(t *testing.T) {
	cases := []struct {
		args   []string
		action Action
		key    string
		value  string
	}{
		{[]string{"config"}, ActionConfigList, "", ""},
		{[]string{"config", "list"}, ActionConfigList, "", ""},
		{[]string{"config", "get", "slots"}, ActionConfigGet, "slots", ""},
		{[]string{"config", "set", "slots", "4"}, ActionConfigSet, "slots", "4"},
		{[]string{"config", "set", "on_finish", "notify-send", "done"}, ActionConfigSet, "on_finish", "notify-send done"},
		{[]string{"config", "edit"}, ActionConfigEdit, "", ""},
		{[]string{"config", "path"}, ActionConfigPath, "", ""},
	}
	for _, tc := range cases {
		cmd, err := Parse(tc.args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", tc.args, err)
		}
		if cmd.Action != tc.action || cmd.EnvKey != tc.key || cmd.EnvValue != tc.value {
			t.Errorf("Parse(%v) = action %v key %q value %q", tc.args, cmd.Action, cmd.EnvKey, cmd.EnvValue)
		}
	}

	for _, bad := range [][]string{
		{"config", "get"},
		{"config", "set", "slots"},
		{"config", "bogus"},
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%v): expected error", bad)
		}
	}
}
