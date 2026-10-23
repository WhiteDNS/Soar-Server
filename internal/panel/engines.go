package panel

import (
	"errors"
	"strings"
)

var engineOrder = []string{"soar", "cotten", "stormdns", "masterdns"}

func enabledEngines(selection string) ([]string, error) {
	switch selection {
	case "", "soar":
		return []string{"soar"}, nil
	case "both": // Preserve existing accounts and API semantics.
		return []string{"soar", "cotten"}, nil
	case "all":
		return append([]string(nil), engineOrder...), nil
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(selection, ",") {
		known := false
		for _, engine := range engineOrder {
			known = known || name == engine
		}
		if !known || seen[name] {
			return nil, errors.New("choose supported engines: soar, cotten, stormdns, masterdns, both or all")
		}
		seen[name] = true
	}
	var result []string
	for _, engine := range engineOrder {
		if seen[engine] {
			result = append(result, engine)
		}
	}
	return result, nil
}

func (c Client) hasEngine(engine string) bool {
	selected, err := enabledEngines(c.Engines)
	if err != nil {
		return false
	}
	for _, name := range selected {
		if name == engine {
			return true
		}
	}
	return false
}

func enginePrefix(engine string) string {
	switch engine {
	case "cotten":
		return "cot."
	case "stormdns":
		return "storm."
	case "masterdns":
		return "master."
	}
	return ""
}
