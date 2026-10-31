package profiles

import "github.com/example/stack-man/agent/internal/commands"

func init() {
	commands.RegisterProfile(commands.Profile{
		Repo:   "github.com/c-lentosc/star-class-hotel-system",
		OnPull: "update",
		Commands: map[string]commands.Def{
			"backup": {
				Description: "Run the hotel backup job",
				Steps: []commands.Step{
					&commands.ExecStep{Service: "backup", Command: "hotel-backup run"},
				},
			},
			"update": {
				Description: "Build images and apply DB migrations",
				Steps: []commands.Step{
					&commands.ComposeStep{Action: "build"},
					&commands.ComposeStep{Action: "up", Flags: []string{"-d", "--build"}},
					&commands.ExecStep{Service: "next", Command: "npx prisma migrate deploy"},
				},
			},
			"restore": {
				Description: "Restore a backup by name",
				Params: []commands.Param{
					{Name: "backup_name", Required: true, Description: "Which backup to restore"},
				},
				Steps: []commands.Step{
					&commands.ExecStep{Service: "backup", Command: "hotel-backup restore ${backup_name}"},
				},
			},
		},
	})
}
