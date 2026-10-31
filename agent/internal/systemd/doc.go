// Package systemd generates the unit file and drives service management.
//
// Responsibilities (phase 1), per docs/agent.md §4:
//   - write /etc/systemd/system/stackman.service
//   - wrap systemctl for start/stop/restart/enable/disable/status
package systemd
