package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// TemplateVars holds the template variable map (upstream
// Dictionary<string,object>: string, []string or nil values).
type TemplateVars map[string]any

// TemplateModifier optionally transforms expanded values (upstream
// TemplateTextModifier, e.g. url.QueryEscape).
type TemplateModifier func(string) string

var (
	// {{ re_replace .Var "pattern" "replacement" }}
	reReplaceRe = regexp.MustCompile(`{{\s*re_replace\s+(\..+?)\s+"(.*?)"\s+"(.*?)"\s*}}`)
	// {{ join .Var "delimiter" }}
	joinRe = regexp.MustCompile(`{{\s*join\s+(\..+?)\s+"(.*?)"\s*}}`)
	// logic functions and/or/eq/ne with 2+ params
	logicRe = regexp.MustCompile(`\b(and|or|eq|ne)(?:\s+(\(?\.[^\)\s]+\)?|"[^"]+")){2,}`)
	// one logic-function parameter: (.Var)|.Var|"literal"
	logicTokenRe = regexp.MustCompile(`\(?\.[^\)\s]+\)?|"[^"]+"`)
	// {{ if .Var }}...{{ else }}...{{ end }}
	ifElseRe = regexp.MustCompile(`{{\s*if\s*(.+?)\s*}}(.*?){{\s*else\s*}}(.*?){{\s*end\s*}}`)
	// {{ range $i, $v := .Var }}prefix{{.}}postfix{{end}}
	rangeRe = regexp.MustCompile(`{{\s*range\s*(((?P<index>\$.+?),)((\s*(?P<element>.+?)\s*(:=)\s*)))?(?P<variable>.+?)\s*}}(?P<prefix>.*?){{\.}}(?P<postfix>.*?){{end}}`)
	// {{ .Var }}
	simpleVarRe = regexp.MustCompile(`{{\s*(\..+?)\s*}}`)
)

// ApplyTemplate ports CardigannIndexer.applyGoTemplateText. Phases run in
// the upstream order: re_replace, join, logic functions, if/else, range,
// simple variables. Missing variables expand to "" (Vestt SPEC-05
// acceptance #3); unknown constructs are left untouched.
func ApplyTemplate(template string, vars TemplateVars, modifier TemplateModifier) string {
	if strings.TrimSpace(template) == "" || !strings.Contains(template, "{{") {
		return template
	}
	if vars == nil {
		vars = TemplateVars{}
	}

	lookup := func(key string) any { return vars[key] }
	asString := func(key string) string {
		v, _ := lookup(key).(string)
		return v
	}

	// re_replace
	for {
		loc := reReplaceRe.FindStringSubmatchIndex(template)
		if loc == nil {
			break
		}
		all := template[loc[0]:loc[1]]
		variable := template[loc[2]:loc[3]]
		pattern := template[loc[4]:loc[5]]
		newvalue := template[loc[6]:loc[7]]
		re, err := regexp.Compile(pattern)
		if err != nil {
			// upstream would throw; leave the construct untouched so a
			// single bad expression cannot kill the whole template
			template = strings.Replace(template, all, "", 1)
			continue
		}
		expanded := re.ReplaceAllString(asString(variable), newvalue)
		if modifier != nil {
			expanded = modifier(expanded)
		}
		template = strings.ReplaceAll(template, all, expanded)
	}

	// join
	for {
		loc := joinRe.FindStringSubmatchIndex(template)
		if loc == nil {
			break
		}
		all := template[loc[0]:loc[1]]
		variable := template[loc[2]:loc[3]]
		delimiter := template[loc[4]:loc[5]]
		items, _ := lookup(variable).([]string)
		expanded := strings.Join(items, delimiter)
		if modifier != nil {
			expanded = modifier(expanded)
		}
		template = strings.ReplaceAll(template, all, expanded)
	}

	// logic functions (and/or/eq/ne) — re-matched from the start after each
	// replacement to support nesting
	for {
		loc := logicRe.FindStringSubmatchIndex(template)
		if loc == nil {
			break
		}
		functionStart := loc[0]
		functionLength := loc[1] - loc[0]
		functionName := template[loc[2]:loc[3]]
		// collect all parameter tokens inside the match (Go's regexp keeps
		// only the last iteration of a repeated group; tokenize manually)
		matchText := template[loc[0]:loc[1]]
		var params []string
		for _, tok := range logicTokenRe.FindAllString(matchText, -1) {
			params = append(params, strings.Trim(tok, "()"))
		}
		functionResult := ""

		usesLiterals := functionName == "eq" || functionName == "ne"
		if !usesLiterals {
			filtered := params[:0]
			for _, p := range params {
				if !strings.HasPrefix(p, "\"") {
					filtered = append(filtered, p)
				}
			}
			params = filtered
		}

		switch functionName {
		case "and", "or": // and: first null/empty; or: first non-empty
			isAnd := functionName == "and"
			for _, p := range params {
				functionResult = p
				empty := strings.TrimSpace(asString(p)) == ""
				if empty == isAnd {
					break
				}
			}
		case "eq", "ne":
			if len(params) > 2 {
				// truncate to the end of the second parameter capture
				toks := logicTokenRe.FindAllStringIndex(matchText, 3)
				if len(toks) >= 3 {
					functionLength = toks[1][1]
				}
			}
			wantEqual := functionName == "eq"
			resolved := make([]string, 0, 2)
			for _, p := range params {
				if len(resolved) == 2 {
					break
				}
				if strings.HasPrefix(p, "\"") {
					resolved = append(resolved, strings.Trim(p, "\""))
				} else {
					resolved = append(resolved, asString(p))
				}
			}
			isEqual := len(resolved) == 2 && resolved[0] == resolved[1]
			if isEqual == wantEqual {
				functionResult = ".True"
			} else {
				functionResult = ".False"
			}
		}

		template = template[:functionStart] + functionResult + template[functionStart+functionLength:]
	}

	// if ... else ... end
	for {
		loc := ifElseRe.FindStringSubmatchIndex(template)
		if loc == nil {
			break
		}
		all := template[loc[0]:loc[1]]
		condition := template[loc[2]:loc[3]]
		onTrue := template[loc[4]:loc[5]]
		onFalse := template[loc[6]:loc[7]]

		conditionResult := ""
		if strings.HasPrefix(condition, ".") {
			var state bool
			switch v := lookup(condition).(type) {
			case nil:
				state = false
			case string:
				state = strings.TrimSpace(v) != ""
			case []string:
				state = len(v) > 0
			default:
				panic(fmt.Sprintf("CardigannIndexer: unexpected type for variable %s: %T", condition, v))
			}
			if state {
				conditionResult = onTrue
			} else {
				conditionResult = onFalse
			}
		} else {
			panic("CardigannIndexer: condition operation '" + condition + "' not implemented")
		}
		template = strings.ReplaceAll(template, all, conditionResult)
	}

	// range
	for {
		loc := rangeRe.FindStringSubmatchIndex(template)
		if loc == nil {
			break
		}
		matchText := template[loc[0]:loc[1]]
		groups := rangeRe.FindStringSubmatch(matchText)
		names := rangeRe.SubexpNames()
		group := func(name string) string {
			for i, n := range names {
				if n == name && i < len(groups) {
					return groups[i]
				}
			}
			return ""
		}
		indexVar := group("index")
		variable := group("variable")
		prefix := group("prefix")
		postfix := group("postfix")

		var expanded strings.Builder
		indexReplace := "{{" + indexVar + "}}"
		items, _ := lookup(variable).([]string)
		for i, item := range items {
			v := item
			if modifier != nil {
				v = modifier(v)
			}
			if indexVar != "" {
				expanded.WriteString(strings.ReplaceAll(prefix, indexReplace, fmt.Sprintf("%d", i)))
				expanded.WriteString(v)
				expanded.WriteString(strings.ReplaceAll(postfix, indexReplace, fmt.Sprintf("%d", i)))
			} else {
				expanded.WriteString(prefix)
				expanded.WriteString(v)
				expanded.WriteString(postfix)
			}
		}
		template = strings.ReplaceAll(template, matchText, expanded.String())
	}

	// simple variables
	for {
		loc := simpleVarRe.FindStringSubmatchIndex(template)
		if loc == nil {
			break
		}
		all := template[loc[0]:loc[1]]
		variable := template[loc[2]:loc[3]]
		value := asString(variable)
		if modifier != nil {
			value = modifier(value)
		}
		template = strings.ReplaceAll(template, all, value)
	}

	return template
}

// ExpandAll expands every string value in the map (helper for inputs and
// headers), preserving order through the caller's iteration.
func ExpandValue(v any, vars TemplateVars, modifier TemplateModifier) string {
	switch t := v.(type) {
	case string:
		return ApplyTemplate(t, vars, modifier)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
