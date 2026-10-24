package panel

// The legacy "both" selection always means Soar+Cotten. Upgrades never opt
// existing accounts into a new engine or silently change their subscription.
var engineSelections = map[string][]string{
	"":                 {"soar"},
	"soar":             {"soar"},
	"cotten":           {"cotten"},
	"both":             {"soar", "cotten"},
	"masterdns":        {"masterdns"},
	"soar_masterdns":   {"soar", "masterdns"},
	"cotten_masterdns": {"cotten", "masterdns"},
	"all":              {"soar", "cotten", "masterdns"},
}

var profileEngines = []string{"soar", "cotten", "masterdns"}

func validEngines(selection string) bool { _, ok := engineSelections[selection]; return ok }
