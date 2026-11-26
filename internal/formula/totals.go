package formula

import (
	"fmt"
	"strconv"
	"strings"
)

// Totals by group (ADR-0090). A QUERY whose columns hold a total answers
// one line for each group: the plain columns say what the lines are
// grouped by, and each total runs over the documents of a group. A total
// is the one a LOOKUP has in its column position (ADR-0046), bound and
// spelled by the same code, so that the sum of a group is the sum a
// LOOKUP gives for the documents of that group. The whole list is one
// statement: the engine groups, and no document is read to be counted
// (ADR-0020).

// GroupTotals are the totals that may stand among the columns of a
// QUERY. MEDIAN is none of them: the statement that finds the middle of
// the rows of a LOOKUP reads them in a way that does not group.
var GroupTotals = []string{"COUNT", "SUM", "MIN", "MAX", "AVERAGE", "COUNTUNIQUE"}

// IsGroupTotal says whether the function, in any case, is one of
// GroupTotals.
func IsGroupTotal(name string) bool {
	for _, t := range GroupTotals {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// BindTotal binds one total of a list of totals: the function over one
// column of the source, as it stands in the column position of a LOOKUP
// over every row. The slot it answers has the type of the total and the
// program of the column; an error is the binder's, such as a sum over a
// text, and its position points nowhere in a formula of the caller's.
func BindTotal(agg, column string, src *Source) (*QuerySlot, error) {
	if !IsGroupTotal(agg) {
		return nil, fmt.Errorf("%s is no total of a group; the totals are %s", agg, strings.Join(GroupTotals, ", "))
	}
	col, ok := src.Column(column)
	if !ok {
		return nil, fmt.Errorf("unknown column %s of %s", column, src.Name)
	}
	scope, err := NewScope()
	if err != nil {
		return nil, err
	}
	text := "LOOKUP(" + src.Name + ", " + strings.ToUpper(agg) + "(" + col.Name + "), TRUE)"
	prog, err := Bind(text, scope, BindOptions{Sources: NewSourceSet(src)})
	if err != nil {
		if fe, ok := err.(*Error); ok {
			return nil, fmt.Errorf("%s", fe.Msg)
		}
		return nil, err
	}
	if len(prog.Queries) != 1 || prog.Queries[0].Agg == "" {
		return nil, fmt.Errorf("%s over %s did not bind as a total", agg, column)
	}
	return &prog.Queries[0], nil
}

// GroupColumn is a plain column of a list of totals: what its lines are
// grouped by.
type GroupColumn struct {
	Name string
	Type Type
}

// TotalsOrder is one term of what a list of totals is sorted by: the
// group or the total at the place Column among the groups, or among the
// totals when Total is set, descending with Desc.
type TotalsOrder struct {
	Column int
	Total  bool
	Desc   bool
}

// The parameters of the statement of a list of totals: how many lines it
// answers at most, and how many it skips.
const (
	TotalsLimit  = "tl"
	TotalsOffset = "to"
)

// TotalsSQL writes the statement of a list of totals by group. Its rows
// are the lines: for each group its value, and after a money one its
// currency; then for each total its value, its currency and its error,
// each NULL when there is none. table is the expression of the rows that
// are counted. The lines come in the order asked for and then in the
// order of their groups, a blank first, so that a page of them is the
// same page each time; TotalsLimit and TotalsOffset say which page.
func TotalsSQL(groups []GroupColumn, totals []*QuerySlot, order []TotalsOrder, d Dialect, table string, params *Params) (string, error) {
	from := func(string) string { return table }
	noArg := func(int, bool) string { return "NULL" }
	var cols, by []string
	// keys are what each group sorts by: its column, and for money its
	// currency after it.
	keys := make([][]string, len(groups))
	for i, g := range groups {
		raw := lookupAlias + "." + quoteIdent(g.Name)
		cols = append(cols, d.Load(raw, g.Type.Kind)+" AS g"+strconv.Itoa(i))
		by, keys[i] = append(by, raw), []string{raw}
		if g.Type.Kind == KindMoney {
			cur := lookupAlias + "." + quoteIdent(g.Name+CurrencySuffix)
			cols = append(cols, cur+" AS g"+strconv.Itoa(i)+"c")
			by, keys[i] = append(by, cur), append(keys[i], cur)
		}
	}
	rows := ""
	for i, q := range totals {
		if q.Agg == "" || !IsGroupTotal(q.Agg) || q.Distinct || q.Limit != nil {
			return "", fmt.Errorf("sqlgen: %s is no total of a group", q.Agg)
		}
		r, err := lookupRows(q, d, noArg, from, params)
		if err != nil {
			return "", err
		}
		a, err := r.aggregate()
		if err != nil {
			return "", err
		}
		if rows == "" {
			rows = a.from
		} else if rows != a.from {
			return "", fmt.Errorf("sqlgen: the totals of a list do not read the same rows")
		}
		orNull := func(x string) string {
			if x == "" {
				return "NULL"
			}
			return x
		}
		n := strconv.Itoa(i)
		cols = append(cols, a.val+" AS t"+n, orNull(a.cur)+" AS t"+n+"c", orNull(coalesceErr(append([]string{a.rowErr}, a.errs...)...))+" AS t"+n+"e")
	}
	if rows == "" {
		return "", fmt.Errorf("sqlgen: a list of totals has no total")
	}
	text := "SELECT " + strings.Join(cols, ", ") + rows
	if len(by) > 0 {
		text += " GROUP BY " + strings.Join(by, ", ")
	}
	// The order: what was asked for, then every group not named yet. A
	// blank stands before every value, in every engine, so its place is
	// written out.
	var terms []string
	named := make([]bool, len(groups))
	place := func(key string, desc bool) string {
		if desc {
			return key + " DESC NULLS LAST"
		}
		return key + " NULLS FIRST"
	}
	for _, o := range order {
		switch {
		case o.Total && o.Column >= 0 && o.Column < len(totals):
			terms = append(terms, place("t"+strconv.Itoa(o.Column), o.Desc))
		case !o.Total && o.Column >= 0 && o.Column < len(groups):
			named[o.Column] = true
			for _, key := range keys[o.Column] {
				terms = append(terms, place(key, o.Desc))
			}
		default:
			return "", fmt.Errorf("sqlgen: a list of totals is sorted by a column it does not have")
		}
	}
	for i := range groups {
		if !named[i] {
			for _, key := range keys[i] {
				terms = append(terms, place(key, false))
			}
		}
	}
	if len(terms) > 0 {
		text += " ORDER BY " + strings.Join(terms, ", ")
	}
	return text + " LIMIT " + d.Param(TotalsLimit) + " OFFSET " + d.Param(TotalsOffset), nil
}
