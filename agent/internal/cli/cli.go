// Package cli defines the cobra commands for one-shot use (phase 1).
//
// Every subcommand is a thin shell over internal/service; the daemon calls the
// same service methods.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/example/stack-man/agent/internal/config"
	"github.com/example/stack-man/agent/internal/git"
	"github.com/example/stack-man/agent/internal/service"
	"github.com/example/stack-man/agent/internal/systemd"
	"github.com/example/stack-man/agent/internal/version"
	"github.com/example/stack-man/pkg/api"
)

// New returns the root command.
func New() *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:           "stackman",
		Short:         "Manage Docker Compose stacks over a private subnet",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", config.DefaultConfigPath, "agent config file")

	root.AddCommand(
		versionCommand(),
		configCommand(&configPath),
		installCommand(),
		uninstallCommand(),
		systemctlCommand("start"),
		systemctlCommand("stop"),
		systemctlCommand("restart"),
		systemctlCommand("enable"),
		systemctlCommand("disable"),
		systemctlCommand("status"),
		stackCommand(&configPath),
		gitCommand(&configPath),
		containersCommand(&configPath),
		repoCommand(&configPath),
	)
	return root
}

// Execute runs the CLI.
func Execute(args []string) error {
	root := New()
	root.SetArgs(args)
	return root.ExecuteContext(context.Background())
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the agent version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "stackman %s (api %d)\n", version.Version, api.APIVersion)
			return nil
		},
	}
}

func configCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Run the interactive configuration wizard",
		RunE: func(*cobra.Command, []string) error {
			return config.Run(*configPath)
		},
	}
}

func installCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the systemd unit and start the daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if err := systemd.New().Install(cmd.Context(), exe); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "installed and started stackman.service")
			return nil
		},
	}
}

func uninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the systemd unit (keeps the data dir)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := systemd.New().Uninstall(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "removed stackman.service; data dir kept")
			return nil
		},
	}
}

func systemctlCommand(verb string) *cobra.Command {
	return &cobra.Command{
		Use:   verb,
		Short: fmt.Sprintf("systemctl %s stackman", verb),
		RunE: func(cmd *cobra.Command, _ []string) error {
			m := systemd.New()
			switch verb {
			case "start":
				return m.Start(cmd.Context())
			case "stop":
				return m.Stop(cmd.Context())
			case "restart":
				return m.Restart(cmd.Context())
			case "enable":
				return m.Enable(cmd.Context())
			case "disable":
				return m.Disable(cmd.Context())
			case "status":
				return m.Status(cmd.Context())
			default:
				return fmt.Errorf("unknown systemctl verb %q", verb)
			}
		},
	}
}

func stackCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stack",
		Short: "Manage registered stacks",
	}

	var add service.AddOptions
	addCmd := &cobra.Command{
		Use:   "add <dir>",
		Short: "Register a git repo with a Compose file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			add.Path = args[0]
			st, warnings, err := svc.AddStack(cmd.Context(), add)
			if err != nil {
				return err
			}
			for _, warning := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "registered %s (%s) on branch %s\n", st.Name, st.Path, branchOrUnknown(st.Branch))
			return nil
		},
	}
	addCmd.Flags().StringVar(&add.Name, "name", "", "stack name (defaults to the directory name)")
	addCmd.Flags().StringVar(&add.Remote, "remote", "", "git remote URL (defaults to origin)")
	addCmd.Flags().StringVar(&add.Branch, "branch", "", "tracked branch (defaults to the current branch)")
	addCmd.Flags().StringSliceVar(&add.Allow, "allow", nil, "comma-separated allowlisted services")
	addCmd.Flags().StringSliceVar(&add.Exclude, "exclude", nil, "comma-separated excluded services")

	removeCmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a stack",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			if err := svc.RemoveStack(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", args[0])
			return nil
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered stacks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			stacks, err := svc.ListStacks(cmd.Context())
			if err != nil {
				return err
			}
			if len(stacks) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no stacks registered")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tBRANCH\tPATH\tREMOTE")
			for _, st := range stacks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", st.Name, branchOrUnknown(st.Branch), st.Path, st.Remote)
			}
			return w.Flush()
		},
	}

	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a stack",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			st, err := svc.GetStack(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "name:         %s\n", st.Name)
			fmt.Fprintf(out, "id:           %s\n", st.ID)
			fmt.Fprintf(out, "path:         %s\n", st.Path)
			fmt.Fprintf(out, "remote:       %s\n", st.Remote)
			fmt.Fprintf(out, "branch:       %s\n", branchOrUnknown(st.Branch))
			fmt.Fprintf(out, "compose:      %s\n", st.ComposeFile)
			fmt.Fprintf(out, "allow:        %s\n", listOrDash(st.Allow))
			fmt.Fprintf(out, "exclude:      %s\n", listOrDash(st.Exclude))
			fmt.Fprintf(out, "created:      %s\n", st.CreatedAt.Format("2006-01-02 15:04:05Z"))
			fmt.Fprintf(out, "updated:      %s\n", st.UpdatedAt.Format("2006-01-02 15:04:05Z"))
			return nil
		},
	}

	servicesCmd := &cobra.Command{
		Use:   "services <name>",
		Short: "Enumerate Compose services and their policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			view, err := svc.Services(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SERVICE\tPOLICY")
			for _, p := range view.Policies {
				policy := p.Policy
				if policy == "" {
					policy = "unspecified"
				}
				fmt.Fprintf(w, "%s\t%s\n", p.Service, policy)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			for _, unknown := range view.Unknown {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: configured service %q is not in the Compose file\n", unknown)
			}
			return nil
		},
	}

	cmd.AddCommand(addCmd, removeCmd, listCmd, showCmd, servicesCmd)
	return cmd
}

func gitCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Git operations for a stack",
	}

	statusCmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show working-tree status (porcelain)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			out, err := svc.GitStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			if out != "" && !strings.HasSuffix(out, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}

	checkCmd := &cobra.Command{
		Use:   "check <name>",
		Short: "Fetch and report whether a pull is safe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			res, err := svc.GitCheck(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printCheck(cmd.OutOrStdout(), res)
			return nil
		},
	}

	fetchCmd := &cobra.Command{
		Use:   "fetch <name>",
		Short: "Fetch the tracked remote branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			if err := svc.GitFetch(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "fetched")
			return nil
		},
	}

	pullCmd := &cobra.Command{
		Use:   "pull <name>",
		Short: "Fast-forward the stack (refuses dirty/diverged trees)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			out := cmd.OutOrStdout()
			res, err := svc.GitPull(cmd.Context(), args[0], out, nil)
			if err != nil {
				return err
			}
			if !res.Pulled {
				fmt.Fprintln(out, "already up to date")
			} else {
				fmt.Fprintf(out, "pulled %s -> %s\n", shortSHA(res.From), shortSHA(res.To))
			}
			return nil
		},
	}

	cmd.AddCommand(statusCmd, checkCmd, fetchCmd, pullCmd)
	return cmd
}

func containersCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "containers",
		Short: "Docker Compose container operations",
	}

	var up service.UpOptions
	upCmd := &cobra.Command{
		Use:   "up <name>",
		Short: "Start the managed services",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			return svc.ContainersUp(cmd.Context(), args[0], up, cmd.OutOrStdout())
		},
	}
	upCmd.Flags().BoolVar(&up.Build, "build", false, "build images before starting")
	upCmd.Flags().BoolVar(&up.ForceRecreate, "force-recreate", false, "recreate containers even if unchanged")
	upCmd.Flags().BoolVar(&up.RemoveOrphans, "remove-orphans", false, "remove containers for services no longer defined")
	upCmd.Flags().StringSliceVar(&up.Services, "services", nil, "target only these managed services")

	var down service.DownOptions
	downCmd := &cobra.Command{
		Use:   "down <name>",
		Short: "Stop the managed services",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			return svc.ContainersDown(cmd.Context(), args[0], down, cmd.OutOrStdout())
		},
	}
	downCmd.Flags().BoolVar(&down.Volumes, "volumes", false, "remove named volumes")
	downCmd.Flags().StringSliceVar(&down.Services, "services", nil, "target only these managed services")

	restartCmd := &cobra.Command{
		Use:   "restart <name> [service...]",
		Short: "Restart the managed services",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			return svc.ContainersRestart(cmd.Context(), args[0], args[1:], cmd.OutOrStdout())
		},
	}

	buildCmd := &cobra.Command{
		Use:   "build <name> [service...]",
		Short: "Build the managed services",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			return svc.ContainersBuild(cmd.Context(), args[0], args[1:], cmd.OutOrStdout())
		},
	}

	psCmd := &cobra.Command{
		Use:   "ps <name>",
		Short: "List the stack's containers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			rows, err := svc.ContainersPs(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSERVICE\tSTATE\tSTATUS")
			for _, row := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", row.Name, row.Service, row.State, row.Status)
			}
			return w.Flush()
		},
	}

	var follow bool
	logsCmd := &cobra.Command{
		Use:   "logs <name> [service]",
		Short: "Stream logs",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			serviceName := ""
			if len(args) > 1 {
				serviceName = args[1]
			}
			return svc.ContainersLogs(cmd.Context(), args[0], serviceName, follow, cmd.OutOrStdout())
		},
	}
	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")

	execCmd := &cobra.Command{
		Use:   "exec <name> <service> -- <cmd...>",
		Short: "Run a command in a managed service",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			res, err := svc.ContainersExec(cmd.Context(), args[0], args[1], args[2:])
			fmt.Fprint(cmd.OutOrStdout(), res.Stdout)
			if res.Stderr != "" {
				fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
			}
			return err
		},
	}

	networkCmd := &cobra.Command{
		Use:   "network",
		Short: "Inspect and attach Docker networks",
	}
	var external bool
	lsCmd := &cobra.Command{
		Use:   "ls [stack]",
		Short: "List Docker networks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			if len(args) == 0 {
				return fmt.Errorf("containers network ls requires a stack name")
			}
			networks, err := svc.ContainersNetworks(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tDRIVER\tSCOPE")
			for _, n := range networks {
				if external && strings.HasPrefix(n.Name, "stackman") {
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", n.Name, n.Driver, n.Scope)
			}
			return w.Flush()
		},
	}
	lsCmd.Flags().BoolVar(&external, "external", false, "hide stackman-created networks")

	connectCmd := &cobra.Command{
		Use:   "connect <name> <network> [service...]",
		Short: "Attach managed containers to a network",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			return svc.NetworkConnect(cmd.Context(), args[0], args[1], args[2:], cmd.OutOrStdout())
		},
	}
	disconnectCmd := &cobra.Command{
		Use:   "disconnect <name> <network> [service...]",
		Short: "Detach managed containers from a network",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			return svc.NetworkDisconnect(cmd.Context(), args[0], args[1], args[2:], cmd.OutOrStdout())
		},
	}
	networkCmd.AddCommand(lsCmd, connectCmd, disconnectCmd)

	cmd.AddCommand(upCmd, downCmd, restartCmd, buildCmd, psCmd, logsCmd, execCmd, networkCmd)
	return cmd
}

func repoCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "command",
		Short: "Run repo-defined and built-in commands",
	}

	listCmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List commands available for a stack",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			infos, err := svc.ListCommands(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSOURCE\tDESCRIPTION")
			for _, info := range infos {
				fmt.Fprintf(w, "%s\t%s\t%s\n", info.Name, info.Source, info.Description)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			// Show params below the table so the table stays readable.
			for _, info := range infos {
				if len(info.Params) == 0 {
					continue
				}
				var parts []string
				for _, p := range info.Params {
					part := p.Name
					if p.Required {
						part += " (required)"
					}
					if p.Default != "" {
						part += "=" + p.Default
					}
					parts = append(parts, part)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s params: %s\n", info.Name, strings.Join(parts, ", "))
			}
			return nil
		},
	}

	var rawParams []string
	runCmd := &cobra.Command{
		Use:   "run <name> <command>",
		Short: "Run a repo or built-in command",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, err := parseParams(rawParams)
			if err != nil {
				return err
			}
			svc, closeFn, err := open(*configPath)
			if err != nil {
				return err
			}
			defer closeFn()
			out := cmd.OutOrStdout()
			progress := func(step, total int, label string) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[%d/%d] %s\n", step, total, label)
			}
			return svc.RunCommand(cmd.Context(), args[0], args[1], params, out, progress)
		},
	}
	runCmd.Flags().StringArrayVar(&rawParams, "param", nil, "command parameter k=v (repeatable)")

	cmd.AddCommand(listCmd, runCmd)
	return cmd
}

func open(configPath string) (*service.Service, func(), error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	svc, err := service.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	return svc, func() { _ = svc.Close() }, nil
}

func printCheck(out io.Writer, res git.CheckResult) {
	status := "UNSAFE"
	if res.Safe {
		status = "SAFE"
	}
	fmt.Fprintf(out, "%s to pull\n", status)
	fmt.Fprintf(out, "branch:  %s\n", res.Branch)
	fmt.Fprintf(out, "head:    %s\n", shortSHA(res.Head))
	fmt.Fprintf(out, "remote:  %s\n", shortSHA(res.RemoteHead))
	fmt.Fprintf(out, "ahead:   %d\n", res.Ahead)
	fmt.Fprintf(out, "behind:  %d\n", res.Behind)
	fmt.Fprintf(out, "clean:   %t\n", res.Clean)
	fmt.Fprintf(out, "diverged:%t\n", res.Diverged)
	if len(res.ChangedFiles) > 0 {
		fmt.Fprintln(out, "changes:")
		files := append([]string(nil), res.ChangedFiles...)
		sort.Strings(files)
		for _, file := range files {
			fmt.Fprintf(out, "  %s\n", file)
		}
	}
}

func parseParams(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, item := range raw {
		key, value, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid --param %q (want k=v)", item)
		}
		out[strings.TrimSpace(key)] = value
	}
	return out, nil
}

func branchOrUnknown(branch string) string {
	if branch == "" {
		return "(unknown)"
	}
	return branch
}

func listOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
