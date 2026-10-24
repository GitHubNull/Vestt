package engine

import (
	"strings"
	"testing"
)

// baseVars mimics GetBaseTemplateVariables' fixed entries: logic functions
// yield {{ .True }}/{{ .False }}, which the later phases resolve as plain
// variables (upstream CardigannIndexer.cs:257-258).
func baseVars(extra TemplateVars) TemplateVars {
	vars := TemplateVars{".True": "True"} // ".False" is nil upstream → absent
	for k, v := range extra {
		vars[k] = v
	}
	return vars
}

// TestApplyTemplate covers SPEC-05 acceptance #3: every applyGoTemplateText
// phase in isolation plus the nested real-world combinations used by the
// shipped definitions.
func TestApplyTemplate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     TemplateVars
		modifier TemplateModifier
		want     string
	}{
		// simple variables
		{"simple var", "q={{ .Keywords }}", TemplateVars{".Keywords": "foo"}, nil, "q=foo"},
		{"missing var becomes empty", "q={{ .Keywords }}!", nil, nil, "q=!"},
		{"modifier applied to var", "{{ .Keywords }}", TemplateVars{".Keywords": "a b"}, func(s string) string { return strings.ToUpper(s) }, "A B"},
		{"no template markers passthrough", "plain text", nil, nil, "plain text"},
		{"empty template", "  ", nil, nil, "  "},
		{"var without spaces", "{{.Keywords}}", TemplateVars{".Keywords": "x"}, nil, "x"},
		{"multiple vars same template", "{{ .A }}/{{ .B }}", TemplateVars{".A": "1", ".B": "2"}, nil, "1/2"},

		// re_replace
		{"re_replace strips pattern", `{{ re_replace .Keywords "[.]" " " }}`, TemplateVars{".Keywords": "S01.E02"}, nil, "S01 E02"},
		{"re_replace capture group", `{{ re_replace .Keywords "(.+)" "[$1]" }}`, TemplateVars{".Keywords": "abc"}, nil, "[abc]"},
		{"re_replace on missing var", `{{ re_replace .Nope "[a]" "b" }}`, nil, nil, ""},
		{"re_replace with modifier", `{{ re_replace .Keywords " " "+" }}`, TemplateVars{".Keywords": "a b"}, func(s string) string { return strings.ToUpper(s) }, "A+B"},
		{"re_replace bad pattern dropped", `pre {{ re_replace .Keywords "(" "x" }} post`, TemplateVars{".Keywords": "v"}, nil, "pre  post"},

		// join
		{"join with delimiter", `{{ join .Categories "," }}`, TemplateVars{".Categories": []string{"1", "2", "3"}}, nil, "1,2,3"},
		{"join multi-char delimiter", `{{ join .Genres " | " }}`, TemplateVars{".Genres": []string{"a", "b"}}, nil, "a | b"},
		{"join empty list", `{{ join .Categories "," }}`, TemplateVars{".Categories": []string{}}, nil, ""},
		{"join missing var", `{{ join .Nope "," }}`, nil, nil, ""},

		// and / or — result is re-inserted as {{ .Param }} and resolved by
		// the simple-variable phase, so top-level use yields the VALUE.
		{"and picks first empty", "{{ and (.Keywords) (.Config.x) }}", baseVars(TemplateVars{".Keywords": "", ".Config.x": "v"}), nil, ""},
		{"and all set picks last", "{{ and (.Keywords) (.Config.x) }}", baseVars(TemplateVars{".Keywords": "k", ".Config.x": "v"}), nil, "v"},
		{"or picks first non-empty", "{{ or (.Keywords) (.Config.x) }}", baseVars(TemplateVars{".Keywords": "", ".Config.x": "v"}), nil, "v"},
		{"or all empty picks last", "{{ or (.Keywords) (.Config.x) }}", baseVars(TemplateVars{".Keywords": "", ".Config.x": ""}), nil, ""},
		{"or literals filtered", `{{ or "lit" (.Keywords) }}`, baseVars(TemplateVars{".Keywords": "k"}), nil, "k"},
		{"and three params", "{{ and (.A) (.B) (.C) }}", baseVars(TemplateVars{".A": "1", ".B": "", ".C": "3"}), nil, ""},

		// eq / ne — yield {{ .True }}/{{ .False }}, resolved via base vars.
		{"eq equal yields True", `{{ eq .Config.sort "seeders" }}`, baseVars(TemplateVars{".Config.sort": "seeders"}), nil, "True"},
		{"eq unequal yields False", `{{ eq .Config.sort "seeders" }}`, baseVars(TemplateVars{".Config.sort": "leechers"}), nil, ""},
		{"ne equal yields False", `{{ ne .Config.sort "seeders" }}`, baseVars(TemplateVars{".Config.sort": "seeders"}), nil, ""},
		{"ne unequal yields True", `{{ ne .Config.sort "seeders" }}`, baseVars(TemplateVars{".Config.sort": "x"}), nil, "True"},
		{"eq extra params truncated", `{{ eq .Config.sort "seeders" "junk" }}`, baseVars(TemplateVars{".Config.sort": "seeders"}), nil, ""}, // upstream leaves a dangling token → KeyNotFound; Vestt tolerates missing vars as ""
		{"eq var vs var", `{{ eq .A .B }}`, baseVars(TemplateVars{".A": "1", ".B": "1"}), nil, "True"},

		// if / else / end
		{"if truthy string", "{{ if .Keywords }}yes{{ else }}no{{ end }}", TemplateVars{".Keywords": "k"}, nil, "yes"},
		{"if empty string else", "{{ if .Keywords }}yes{{ else }}no{{ end }}", TemplateVars{".Keywords": ""}, nil, "no"},
		{"if missing var else", "{{ if .Keywords }}yes{{ else }}no{{ end }}", nil, nil, "no"},
		{"if non-empty slice", "{{ if .Categories }}yes{{ else }}no{{ end }}", TemplateVars{".Categories": []string{"1"}}, nil, "yes"},
		{"if empty slice else", "{{ if .Categories }}yes{{ else }}no{{ end }}", TemplateVars{".Categories": []string{}}, nil, "no"},

		// nested: real 1337x pattern — logic result feeds if condition
		{"if and eq nested", `{{ if and (.Keywords) (eq .Config.disablesort "false") }}sorted{{ else }}plain{{ end }}`,
			baseVars(TemplateVars{".Keywords": "k", ".Config.disablesort": "false"}), nil, "sorted"},
		{"if and eq nested false arm", `{{ if and (.Keywords) (eq .Config.disablesort "false") }}sorted{{ else }}plain{{ end }}`,
			baseVars(TemplateVars{".Keywords": "", ".Config.disablesort": "false"}), nil, "plain"},
		{"if or nested", `{{ if or (.Result.date_year) (.Result.date_today) }}{{ .Result.date_year }}{{ else }}nodate{{ end }}`,
			baseVars(TemplateVars{".Result.date_year": "2020", ".Result.date_today": ""}), nil, "2020"},
		{"if or nested both empty", `{{ if or (.Result.date_year) (.Result.date_today) }}{{ .Result.date_year }}{{ else }}nodate{{ end }}`,
			baseVars(TemplateVars{".Result.date_year": "", ".Result.date_today": ""}), nil, "nodate"},

		// range
		{"range without index", "{{ range .Categories }}cat={{.}};{{end}}", TemplateVars{".Categories": []string{"1", "2"}}, nil, "cat=1;cat=2;"},
		{"range with index", "{{ range $i, $v := .Categories }}{{$i}}:{{.}};{{end}}", TemplateVars{".Categories": []string{"a", "b"}}, nil, "0:a;1:b;"},
		{"range empty", "{{ range .Categories }}x{{.}}{{end}}", TemplateVars{".Categories": []string{}}, nil, ""},
		{"range with modifier", "{{ range .Categories }}{{.}}{{end}}", TemplateVars{".Categories": []string{"a", "b"}}, func(s string) string { return strings.ToUpper(s) }, "AB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ApplyTemplate(tt.template, tt.vars, tt.modifier)
			if got != tt.want {
				t.Errorf("ApplyTemplate(%q)\n got: %q\nwant: %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestExpandValue(t *testing.T) {
	t.Parallel()
	if got := ExpandValue("{{ .K }}", TemplateVars{".K": "v"}, nil); got != "v" {
		t.Errorf("string: got %q", got)
	}
	if got := ExpandValue(nil, nil, nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
	if got := ExpandValue(42, nil, nil); got != "42" {
		t.Errorf("int: got %q", got)
	}
}
