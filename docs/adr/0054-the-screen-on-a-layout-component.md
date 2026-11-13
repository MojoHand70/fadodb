# ADR-0054: The screen on a layout component: `mobile` and `largescreen`

- Status: Proposed; amends ADR-0018 (two properties of a component that are not formulas)
- Date: 2026-11-13

## Context

A layout is the face of a document: a named list of components, each
with the property formulas `visible`, `enabled`, `editable` and `style`
(ADR-0018, with the components of ADR-0032 and ADR-0034). `visible` is
the owner's requirement from the start of the project: every visual
element is shown or not by a condition of its own, and the condition
belongs to the element and not to a variable. One variable may stand in
two components, and a button or a heading has no variable at all.

Those formulas answer from the document and from whoever looks at it:
its state, its values, the caller. None of them can ask how large the
screen is. A person approves an invoice on a phone and reads its forty
lines at a desk. On the phone the wide table of lines is in the way,
and a single line that counts them serves better; at the desk it is the
other way round. The owner's earlier system had a switch for this on
every widget, and on 2026-11-13 he asked for it here as two properties
that are true or false, `mobile` and `largescreen`.

Layouts are not built yet. The store keeps the name `layouts` for their
table, and they come with the server in Phase 6 and the renderer in
Phase 7. This record settles the two properties before the first layout
is saved.

Two things already decided bound the answer. A formula answers the same
on the Go VM, in SQL and in the browser (ADR-0019), and only the
browser has a screen. And the caller is as often an agent as a person,
with no screen at all, so no command may depend on a layout.

## Decision

1. **Two properties.** A component may declare `mobile` and
   `largescreen`. Each is `true` or `false`, written as a JSON literal.
   One that is left out is `true`. They are not formulas.

   ```json
   {
     "class": "Invoice",
     "name": "form",
     "components": [
       {"type": "field",  "bind": "customer"},
       {"type": "grid",   "bind": "lines",      "mobile": false},
       {"type": "field",  "bind": "line_count", "largescreen": false},
       {"type": "button", "transition": "approve", "visible": "STATE() = \"review\""}
     ]
   }
   ```

   The words `type`, `bind` and `transition` make the example readable.
   ADR-0018 names the properties of a component and not the keys of its
   kind and its variable, and this record fixes neither.

2. **When a component shows.** A frontend calls the screen it draws on
   mobile or large: one of the two, never both and never neither. A
   component shows when its `visible` formula is TRUE, or it has none,
   and its property for that screen is `true`. For the layout above:

   | Component | `visible` | `mobile` | `largescreen` | On a phone | At a desk |
   |---|---|---|---|---|---|
   | `customer` | none | left out, so `true` | left out, so `true` | shown | shown |
   | `lines` | none | `false` | left out, so `true` | hidden | shown |
   | `line_count` | none | left out, so `true` | `false` | shown | hidden |
   | `approve`, invoice in `review` | TRUE | left out, so `true` | left out, so `true` | shown | shown |
   | `approve`, invoice in `draft` | FALSE | left out, so `true` | left out, so `true` | hidden | hidden |

3. **Which screen is which is the frontend's.** The engine does not
   know the screen and no command carries it. A frontend decides by the
   width it has to draw in and not by the make of the device, so a
   narrow window at a desk is mobile and a tablet is whichever its
   width makes it, and it decides again when the window is resized or
   the device is turned. There are two screens, in the owner's words. A
   third is not added.

4. **A component that holds others.** Where a component holds other
   components, as a section or a tab does, one that is hidden for a
   screen hides everything it holds, whatever their own properties say.

5. **No function of the screen.** The formula language gains nothing
   that asks about the screen. `visible` goes on speaking of the
   document and the caller, and the two properties speak of the screen.
   This is why they are literals: a function whose answer only the
   browser has could not be the same on three evaluators.

6. **Checked when the layout is saved.** A value that is not `true` or
   `false`, a formula or a text among them, is refused, as a binding to
   a variable the class does not have is refused (ADR-0018). A
   component with both set to `false` is never drawn. The layout is
   saved, and the answer carries a note that names the component, as a
   rule left out is noted (ADR-0052, item 5).

7. **They protect nothing.** The document on the wire, the bundle and
   every command are the same on every screen. What a person may see
   and set is decided by the read rule and the rights table (ADR-0028,
   ADR-0049), whatever they hold in their hand. A value whose component
   is off for the phone is still sent to the phone, and may be set from
   one by any caller the rights let. A check at commit, a guard or a
   range, does not ask which components were on the screen.

8. **Where they live.** In the layout JSON, so they are versioned with
   the layout and never cause a class version or a migration, and they
   travel in the bundle under its ETag (ADR-0018). The components of
   ADR-0032 and ADR-0034 take them as any component does.

9. **What this record leaves out.**

   - A different arrangement for the phone: another order, other
     groups. The two properties are for the same arrangement with fewer
     parts. Another arrangement is a second named layout, which
     ADR-0018 allows, and nothing yet says how a frontend chooses among
     the layouts of a class (question B).
   - A third size, the way the device is held, and paper.
   - What one person chooses to hide for themselves. That is the
     frontend's, and no layout keeps it.

## Questions for the owner

A. **The spelling.** The record writes `largescreen`, the owner's word,
   and the other properties of a component are one word each. Where the
   formats of the engine put two words in a key they join them with an
   underscore (`created_at`, `values_change`), which would give
   `large_screen`. The record keeps `largescreen`.

B. **The same two properties on a whole layout.** A layout that says
   `"mobile": false` would not be offered on a phone, so a class could
   keep `form` for the desk and `form-phone` for the phone, and a
   frontend would know which to open. It is one sentence in item 1. The
   record leaves it out until the owner says.

## Consequences

- A designer, a person or an agent, says once on each component where
  it shows. Whoever reads a layout sees what a phone gets without
  evaluating a formula.
- Two things now decide whether a component shows, where one did. The
  table of item 2 is the whole rule.
- A commit from a phone may be refused over a value the phone does not
  show, by a guard that reads `lines` for one. The refusal names what
  refused, as on any screen. The designer keeps on each screen what the
  buttons shown there need, and the engine does not check it.
- Two frontends may draw the line between mobile and large at different
  widths, so one layout may show differently in each at the same width.
  The engine has no screen and cannot settle it.
- A frontend that does not know the two properties shows every
  component its `visible` allows. Nothing is lost and nothing leaks,
  since they protect nothing.

## Performance

Nothing in the commit path, the store or the three evaluators. In the
browser a component whose property for the screen is `false` evaluates
none of its formulas, so a layout costs no more on a phone than at a
desk, inside the 1 ms for 200 formulas of ADR-0012. The bundle grows by
at most two literals per component.
