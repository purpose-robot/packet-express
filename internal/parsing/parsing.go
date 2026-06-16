package parsing

import (
	"embed"
	"fmt"

	"github.com/sirikothe/gotextfsm"
)

//go:embed templates/*.textfsm
var templates embed.FS

func Template(name, text string) ([]map[string]any, error) {
	parsedTemplate, err := templates.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("failed to read TextFSM template %s from directory; %w", name, err)
	}

	fsm := gotextfsm.TextFSM{}
	if err := fsm.ParseString(string(parsedTemplate)); err != nil {
		return nil, fmt.Errorf("failed to parse TextFSM template %s from directory; %w", name, err)
	}

	parser := gotextfsm.ParserOutput{}
	if err := parser.ParseTextString(text, fsm, true); err != nil {
		return nil, fmt.Errorf("failed to parse output data from provided network device; %w", err)
	}

	return parser.Dict, nil
}
