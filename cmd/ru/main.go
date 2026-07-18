package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/han/qrush/internal/cli"
	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/format"
	"github.com/han/qrush/internal/server"
	"github.com/han/qrush/internal/tui"
)

func main() {
	cmd, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ru: %v\n", err)
		os.Exit(1)
	}

	switch cmd.Action {
	case cli.ActionShowHelp:
		cli.ShowHelp()
		return
	case cli.ActionShowVersion:
		cli.ShowVersion()
		return
	case cli.ActionServerMode:
		runServer()
		return
	case cli.ActionConfigList, cli.ActionConfigGet, cli.ActionConfigSet,
		cli.ActionConfigEdit, cli.ActionConfigPath:
		// Config commands read/write local files; they must not auto-start
		// (or require) a daemon.
		if err := doConfig(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "ru: %v\n", err)
			os.Exit(1)
		}
		return
	case cli.ActionInteractive, cli.ActionJobsView:
		if err := client.EnsureServer(); err != nil {
			fmt.Fprintf(os.Stderr, "ru: %v\n", err)
			os.Exit(1)
		}
		// When `ru` / `ru -S` / `ru --jobs` is launched from inside a qrush-hosted
		// pane, spawning a nested TUI would open a session inside a session. Instead,
		// ask the already-running interactive TUI to surface its management view.
		if os.Getenv("QRUSH_SESSION") != "" {
			if err := client.RequestJobsView(); err != nil {
				fmt.Fprintf(os.Stderr, "ru: %v\n", err)
				os.Exit(1)
			}
			return
		}
		run := tui.Run
		if cmd.Action == cli.ActionJobsView {
			run = tui.RunJobs
		}
		if err := run(); err != nil {
			fmt.Fprintf(os.Stderr, "ru: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := client.EnsureServer(); err != nil {
		fmt.Fprintf(os.Stderr, "ru: %v\n", err)
		os.Exit(1)
	}

	if err := dispatch(cmd); err != nil {
		fmt.Fprintf(os.Stderr, "ru: %v\n", err)
		os.Exit(1)
	}
}

func runServer() {
	server.IgnoreSIGPIPE()
	cfg := config.Load()
	srv, err := server.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ru server: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Shutdown()
		cancel()
	}()

	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ru server: %v\n", err)
		os.Exit(1)
	}
}

func dispatch(cmd *cli.Command) error {
	switch cmd.Action {
	case cli.ActionList:
		return doList(cmd)
	case cli.ActionQueue:
		return doQueue(cmd)
	case cli.ActionForeground:
		return doForeground(cmd)
	case cli.ActionKillServer:
		return client.KillServer()
	case cli.ActionClearFinished:
		if cmd.Session != "" {
			return client.ClearFinishedInSession(cmd.Session)
		}
		return client.ClearFinished()
	case cli.ActionCatOutput:
		return doWithJobID(cmd, func(id int) error { return client.CatOutput(id) })
	case cli.ActionShowOutputFile:
		return doWithJobID(cmd, func(id int) error { return client.ShowOutputFile(id) })
	case cli.ActionTail:
		return doWithJobID(cmd, func(id int) error { return client.TailOutput(id) })
	case cli.ActionShowPID:
		return doWithJobID(cmd, func(id int) error {
			pid, err := client.GetPID(id)
			if err != nil {
				return err
			}
			fmt.Println(pid)
			return nil
		})
	case cli.ActionInfo:
		return doWithJobID(cmd, func(id int) error {
			info, err := client.GetInfo(id)
			if err != nil {
				return err
			}
			fmt.Print(format.FormatJobInfo(info))
			return nil
		})
	case cli.ActionGetState:
		return doWithJobID(cmd, func(id int) error {
			state, err := client.GetState(id)
			if err != nil {
				return err
			}
			fmt.Println(state)
			return nil
		})
	case cli.ActionRemoveJob:
		return doWithJobID(cmd, func(id int) error { return client.RemoveJob(id) })
	case cli.ActionRerun:
		return doWithJobID(cmd, func(id int) error {
			newID, err := client.Rerun(id)
			if err != nil {
				return err
			}
			fmt.Println(newID)
			return nil
		})
	case cli.ActionWaitJob:
		return doWithJobID(cmd, func(id int) error {
			result, err := client.WaitJob(id)
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				os.Exit(result.ExitCode)
			}
			return nil
		})
	case cli.ActionKillJob:
		return doWithJobID(cmd, func(id int) error { return client.KillJob(id) })
	case cli.ActionKillAll:
		return client.KillAllJobs()
	case cli.ActionUrgent:
		return doWithJobID(cmd, func(id int) error { return client.MakeUrgent(id) })
	case cli.ActionSwapJobs:
		if cmd.JobID < 0 || cmd.JobID2 < 0 {
			return fmt.Errorf("swap requires two job IDs: -U id1-id2")
		}
		return client.SwapJobs(cmd.JobID, cmd.JobID2)
	case cli.ActionSetMaxSlots:
		return client.SetMaxSlots(cmd.Slots)
	case cli.ActionGetMaxSlots:
		slots, err := client.GetMaxSlots()
		if err != nil {
			return err
		}
		fmt.Println(slots)
		return nil
	case cli.ActionCountRunning:
		count, err := client.CountRunning()
		if err != nil {
			return err
		}
		fmt.Println(count)
		return nil
	case cli.ActionGetLabel:
		return doWithJobID(cmd, func(id int) error {
			label, err := client.GetLabel(id)
			if err != nil {
				return err
			}
			fmt.Println(label)
			return nil
		})
	case cli.ActionLastID:
		id, err := client.LastID()
		if err != nil {
			return err
		}
		fmt.Println(id)
		return nil
	case cli.ActionShowCmd:
		return doWithJobID(cmd, func(id int) error {
			c, err := client.GetCmd(id)
			if err != nil {
				return err
			}
			fmt.Println(c)
			return nil
		})
	case cli.ActionGetEnv:
		val, err := client.GetEnv(cmd.EnvKey)
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	case cli.ActionSetEnv:
		return client.SetEnv(cmd.EnvKey, cmd.EnvValue)
	case cli.ActionUnsetEnv:
		return client.UnsetEnv(cmd.EnvKey)
	case cli.ActionGetLogdir:
		dir, err := client.GetLogdir()
		if err != nil {
			return err
		}
		fmt.Println(dir)
		return nil
	case cli.ActionSetLogdir:
		return client.SetLogdir(cmd.LogdirPath)
	case cli.ActionSessionList:
		sessions, err := client.SessionList()
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(sessions, "\n"))
		return nil
	case cli.ActionSessionCreate:
		return client.SessionCreate(cmd.Session)
	case cli.ActionSessionRename:
		return client.SessionRename(cmd.Session, cmd.SessionArg)
	case cli.ActionSessionDelete:
		return client.SessionDelete(cmd.Session)
	case cli.ActionSessionMove:
		return client.SessionMove(cmd.Session, cmd.SessionArg)
	case cli.ActionGroupList:
		groups, err := client.GroupList()
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(groups, "\n"))
		return nil
	case cli.ActionGroupCreate:
		return client.GroupCreate(cmd.Session)
	case cli.ActionGroupRename:
		return client.GroupRename(cmd.Session, cmd.SessionArg)
	case cli.ActionGroupDelete:
		return client.GroupDelete(cmd.Session)
	case cli.ActionTermList:
		terms, err := client.ListTerminals()
		if err != nil {
			return err
		}
		for _, t := range terms {
			fmt.Printf("%s\t%s\n", t.Session, t.Pane)
		}
		return nil
	case cli.ActionTermKill:
		return client.KillTerminal(cmd.Session, cmd.SessionArg)
	default:
		return fmt.Errorf("unknown action")
	}
}

func doList(cmd *cli.Command) error {
	var result *client.ListResult
	var err error
	if cmd.Session != "" {
		result, err = client.ListJobsInSession(cmd.Session)
	} else {
		result, err = client.ListJobs()
	}
	if err != nil {
		return err
	}
	output := format.FormatJobList(result.Jobs, result.MaxSlots, cmd.ListFormat)
	if output != "" {
		fmt.Print(output)
	}
	return nil
}

func doQueue(cmd *cli.Command) error {
	if len(cmd.Command) == 0 {
		return fmt.Errorf("no command specified")
	}
	if err := validateSupportedOptions(cmd); err != nil {
		return err
	}

	dependOn := cmd.DependOn
	if len(dependOn) > 0 {
		for i, id := range dependOn {
			if id == -1 {
				lastID, err := client.LastID()
				if err != nil {
					return fmt.Errorf("get last job id: %w", err)
				}
				dependOn[i] = lastID
			}
		}
	}

	opts := client.SubmitOpts{
		StoreOutput:    cmd.StoreOutput,
		SeparateStderr: cmd.SeparateStderr,
		GzipOutput:     cmd.GzipOutput,
		DependOn:       dependOn,
		RequireElevel:  cmd.RequireElevel,
		Label:          cmd.Label,
		Session:        cmd.Session,
		Message:        cmd.Message,
		NumSlots:       cmd.NumSlots,
		Logfile:        cmd.Logfile,
	}

	id, err := client.SubmitJob(cmd.Command, opts)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func doForeground(cmd *cli.Command) error {
	if len(cmd.Command) == 0 {
		return fmt.Errorf("no command specified")
	}
	if err := validateSupportedOptions(cmd); err != nil {
		return err
	}

	dependOn := cmd.DependOn
	if len(dependOn) > 0 {
		for i, id := range dependOn {
			if id == -1 {
				lastID, err := client.LastID()
				if err != nil {
					return fmt.Errorf("get last job id: %w", err)
				}
				dependOn[i] = lastID
			}
		}
	}

	opts := client.SubmitOpts{
		StoreOutput:    cmd.StoreOutput,
		SeparateStderr: cmd.SeparateStderr,
		GzipOutput:     cmd.GzipOutput,
		DependOn:       dependOn,
		RequireElevel:  cmd.RequireElevel,
		Label:          cmd.Label,
		Session:        cmd.Session,
		Message:        cmd.Message,
		NumSlots:       cmd.NumSlots,
		Logfile:        cmd.Logfile,
	}

	id, err := client.SubmitJob(cmd.Command, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "queued job %d, waiting...\n", id)

	result, err := client.WaitJob(id)
	if err != nil {
		return err
	}

	if cmd.StoreOutput {
		if catErr := client.CatOutput(id); catErr != nil {
			// output may not exist yet
		}
	}

	if result.ExitCode != 0 {
		os.Exit(result.ExitCode)
	}
	return nil
}

func doConfig(cmd *cli.Command) error {
	switch cmd.Action {
	case cli.ActionConfigPath:
		fmt.Println(config.FilePath())
		return nil

	case cli.ActionConfigList:
		_, settings, warnings := config.LoadDetailed()
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "ru: warning: %s\n", w)
		}
		tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE\tDESCRIPTION")
		for _, s := range settings {
			val := s.Value
			if val == "" {
				val = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Key.Name, val, s.Source, s.Key.Desc)
		}
		return tw.Flush()

	case cli.ActionConfigGet:
		if _, ok := config.KeyByName(cmd.EnvKey); !ok {
			return fmt.Errorf("unknown config key %q (see: ru config list)", cmd.EnvKey)
		}
		_, settings, _ := config.LoadDetailed()
		for _, s := range settings {
			if s.Key.Name == cmd.EnvKey {
				fmt.Println(s.Value)
			}
		}
		return nil

	case cli.ActionConfigSet:
		key, value := cmd.EnvKey, cmd.EnvValue
		k, ok := config.KeyByName(key)
		if !ok {
			return fmt.Errorf("unknown config key %q (see: ru config list)", key)
		}
		if k.IsInt {
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s needs an integer, got %q", key, value)
			}
			if n < k.MinInt {
				return fmt.Errorf("%s must be at least %d", key, k.MinInt)
			}
		}
		if err := config.SetFileValue(key, value); err != nil {
			return err
		}
		// The file now carries the user's latest intent; drop any stale
		// runtime override so it actually takes effect.
		if err := config.DeleteRuntime(key); err != nil {
			return err
		}
		if os.Getenv(k.EnvVar) != "" {
			fmt.Fprintf(os.Stderr, "ru: note: $%s is set and overrides the config file\n", k.EnvVar)
		}
		applyConfigLive(key, value)
		return nil

	case cli.ActionConfigEdit:
		path := config.FilePath()
		if path == "" {
			return fmt.Errorf("cannot determine config path (no home directory)")
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(config.FileTemplate()), 0o600); err != nil {
				return err
			}
		}
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			editor = "vi"
		}
		ed := exec.Command(editor, path)
		ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
		return ed.Run()
	}
	return nil
}

// applyConfigLive pushes a changed setting to an already-running daemon so
// `ru config set` takes effect immediately. A daemon that isn't running is
// fine — the setting is picked up on its next start.
func applyConfigLive(key, value string) {
	switch key {
	case "slots":
		if n, err := strconv.Atoi(value); err == nil {
			if err := client.SetMaxSlots(n); err == nil {
				fmt.Fprintln(os.Stderr, "ru: applied to the running daemon")
			}
		}
	case "logdir":
		if err := client.SetLogdir(value); err == nil {
			fmt.Fprintln(os.Stderr, "ru: applied to the running daemon")
		}
	}
}

func validateSupportedOptions(cmd *cli.Command) error {
	if cmd.GzipOutput {
		return fmt.Errorf("-z is not implemented")
	}
	if cmd.NonBlocking {
		return fmt.Errorf("-B is not implemented")
	}
	return nil
}

func doWithJobID(cmd *cli.Command, fn func(int) error) error {
	id := cmd.JobID
	if id < 0 {
		var err error
		id, err = client.LastID()
		if err != nil {
			return fmt.Errorf("get last job id: %w", err)
		}
	}
	return fn(id)
}
