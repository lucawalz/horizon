package capi

import (
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
)

func RenderFlavor(template []byte, vars map[string]string) ([]byte, error) {
	p := yamlprocessor.NewSimpleProcessor()
	required, err := p.GetVariables(template)
	if err != nil {
		return nil, fmt.Errorf("capi: inspect flavor variables: %w", err)
	}
	var missing []string
	for _, name := range required {
		if _, ok := vars[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("capi: flavor missing required variables: %s", strings.Join(missing, ", "))
	}
	out, err := p.Process(template, func(name string) (string, error) {
		value, ok := vars[name]
		if !ok {
			return "", fmt.Errorf("variable %q not set", name)
		}
		return value, nil
	})
	if err != nil {
		return nil, fmt.Errorf("capi: process flavor: %w", err)
	}
	return out, nil
}
