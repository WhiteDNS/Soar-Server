// Package managementassets carries the SSH menu in the verified operations
// binary so updates from older installers can add the new command as well.
package managementassets

import _ "embed"

//go:embed panel-menu.sh
var MenuScript []byte
