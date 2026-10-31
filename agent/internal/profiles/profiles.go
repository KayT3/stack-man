// Package profiles contains the repo-specific command sets compiled into the
// agent. Add one file per repository and call commands.RegisterProfile in
// init(). Profiles are keyed by the normalized git remote; see
// commands.NormalizeRemote.
package profiles
