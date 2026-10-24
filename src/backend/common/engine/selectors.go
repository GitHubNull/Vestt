package engine

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// containsPseudoRe matches the AngleSharp jQuery-extension :contains()
// pseudo-class (quoted or bare argument) used by 341 definitions.
var containsPseudoRe = regexp.MustCompile(`:contains\(\s*(?:"([^"]*)"|'([^']*)'|([^)"']+?))\s*\)`)

// handleSelector ports CardigannIndexer.handleSelector (CardigannIndexer.cs
// 1291-1366). dom is the document (or element) to search; when required is
// false, a non-matching selector returns ("", nil) instead of erroring.
func (x *Cardigann) handleSelector(sel *SelectorBlock, dom *goquery.Selection, vars TemplateVars, required bool) (string, error) {
	if sel == nil {
		return "", nil
	}
	if sel.Text != "" {
		return x.applyFilters(ApplyTemplate(sel.Text, vars, nil), sel.Filters, vars), nil
	}

	selection := dom
	var value string

	if sel.Selector != "" {
		selectorText := ApplyTemplate(sel.Selector, vars, nil)
		selection = querySelector(dom, selectorText)
		if selection == nil || selection.Length() == 0 {
			if required {
				return "", fmt.Errorf("Selector %q didn't match", selectorText)
			}
			return "", nil
		}
	}

	if sel.Remove != "" {
		selection.Find(sel.Remove).Remove()
	}

	if len(sel.Case) > 0 {
		for _, switchCase := range sel.Case {
			if matchesOrContains(selection, switchCase.Key) {
				value = ApplyTemplate(switchCase.Value, vars, nil)
				break
			}
		}
		if value == "" {
			if required {
				keys := make([]string, 0, len(sel.Case))
				for _, c := range sel.Case {
					keys = append(keys, c.Key)
				}
				return "", fmt.Errorf("None of the case selectors %q matched", strings.Join(keys, ","))
			}
			return "", nil
		}
	} else if sel.Attribute != "" {
		var ok bool
		value, ok = selection.Attr(sel.Attribute)
		if !ok {
			if required {
				return "", fmt.Errorf("Attribute %q is not set for element", sel.Attribute)
			}
			return "", nil
		}
	} else {
		value = selection.Text()
	}

	return x.applyFilters(normalizeSpace(value), sel.Filters, vars), nil
}

// querySelector ports CardigannIndexer.QuerySelector (1277-1289): the :root
// pseudo selector climbs to the top-level element before matching.
func querySelector(dom *goquery.Selection, selector string) *goquery.Selection {
	return querySelectorAll(dom, selector).First()
}

// querySelectorAll is QuerySelectorAll with the :contains pseudo-class
// emulated: cascadia does not implement it (AngleSharp does upstream), so a
// selector containing it is evaluated compound-by-compound, applying each
// :contains() as a subtree-text filter on the elements matched by its own
// compound. Combinators ">", "+", "~" inside such selectors fall back to
// descendant matching (approximation, see SPEC-06 澄清记录).
func querySelectorAll(dom *goquery.Selection, selector string) *goquery.Selection {
	if strings.HasPrefix(selector, ":root") {
		selector = selector[len(":root"):]
		for len(dom.Parent().Nodes) > 0 {
			dom = dom.Parent()
		}
	}
	if selector == "" {
		return dom
	}
	if !strings.Contains(selector, ":contains(") {
		return dom.Find(selector)
	}
	var nodes []*html.Node
	seen := map[*html.Node]bool{}
	for _, group := range splitSelectorGroups(selector) {
		for _, n := range querySelectorAllContains(dom, group).Nodes {
			if !seen[n] {
				seen[n] = true
				nodes = append(nodes, n)
			}
		}
	}
	return &goquery.Selection{Nodes: nodes}
}

// querySelectorAllContains evaluates one comma-free selector with :contains
// emulated compound-by-compound.
func querySelectorAllContains(dom *goquery.Selection, selector string) *goquery.Selection {
	cur := dom
	for _, compound := range splitCompounds(selector) {
		if compound == ">" || compound == "+" || compound == "~" {
			continue
		}
		matches := containsPseudoRe.FindAllStringSubmatch(compound, -1)
		base := strings.TrimSpace(containsPseudoRe.ReplaceAllString(compound, ""))
		if base == "" {
			base = "*"
		}
		next := cur.Find(base)
		for _, g := range matches {
			needle := g[1]
			if needle == "" {
				needle = g[2]
			}
			if needle == "" {
				needle = strings.TrimSpace(g[3])
			}
			next = next.FilterFunction(func(_ int, s *goquery.Selection) bool {
				return strings.Contains(s.Text(), needle)
			})
		}
		cur = next
	}
	return cur
}

// splitSelectorGroups splits a selector on top-level commas (outside
// brackets, parentheses and quotes), yielding comma-free selector groups.
func splitSelectorGroups(sel string) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	flush := func(end int) {
		if c := strings.TrimSpace(sel[start:end]); c != "" {
			out = append(out, c)
		}
	}
	for i := 0; i < len(sel); i++ {
		c := sel[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(sel))
	return out
}

// splitCompounds splits a selector on top-level whitespace (outside
// brackets, parentheses and quotes), yielding compound fragments.
func splitCompounds(sel string) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	flush := func(end int) {
		if c := strings.TrimSpace(sel[start:end]); c != "" {
			out = append(out, c)
		}
	}
	for i := 0; i < len(sel); i++ {
		c := sel[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case ' ', '\t', '\n', '\r':
			if depth == 0 {
				flush(i)
				for i+1 < len(sel) && strings.ContainsRune(" \t\n\r", rune(sel[i+1])) {
					i++
				}
				start = i + 1
			}
		}
	}
	flush(len(sel))
	return out
}

// matchesOrContains mirrors selection.Matches(case) || QuerySelector(case) != null.
// parseHTML parses an HTML document string.
func parseHTML(s string) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(s))
}

// goqueryOuterHTML renders a selection's outer HTML (debug logging).
func goqueryOuterHTML(sel *goquery.Selection) string {
	if sel == nil || sel.Length() == 0 {
		return "<NULL>"
	}
	html, err := goquery.OuterHtml(sel)
	if err != nil {
		return "<ERROR>"
	}
	return html
}

func matchesOrContains(sel *goquery.Selection, css string) bool {
	if sel.Length() == 0 {
		return false
	}
	if sel.Is(css) {
		return true
	}
	return querySelectorAll(sel, css).Length() > 0
}

// normalizeSpace mirrors ParseUtil.NormalizeSpace.
func normalizeSpace(s string) string { return strings.TrimSpace(s) }
