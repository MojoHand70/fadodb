# FadoDB skills for agents

The agent is the primary caller of FadoDB. Some processes it runs will
never have a human interface. These skills are written for that caller:
each one tells an agent how to do one kind of work with FadoDB well, in
the SKILL.md format that Claude Code, claude.ai and other agent runtimes
load when a task matches a skill's description.

A skill is procedure and knowledge. The verbs are elsewhere: the Go API
today, and each of its calls as a JSON command through the Go call
`Exec`; the server that carries the commands over the network; from a
shell, `fadodb exec`; and for an agent whose program speaks the Model
Context Protocol, `fadodb mcp`, which offers each command as a tool. A skill tells the agent to preview before it
commits, to carry an idempotency key, to read a rejection's code and
location rather than guess; the API is what it then calls.

**Status on 2026-11-14: all five skills are written.** Each tells the
agent what to read in the manual under `../docs/`, shows one piece of
work with the engine's own answers, and lists what goes wrong most
often. The engine defines the class each skill shows in a test on
each push, and for the operator's, the analyst's, the simulator's and
the own-work skill the same test sends the commands of the page and
compares the answers. The shared reference set is the manual: the
class format, the formula language, the functions, the commands and
the refusals are chapters of it. The sample corpus under
`../examples/classes/` holds four classes.

## The five hats

An agent wears one of five hats when it works with FadoDB. One skill each,
so the right one loads for the task at hand.

| Hat | Skill | What it does | Today |
|---|---|---|---|
| Designer | [`fadodb-design/`](fadodb-design/SKILL.md) | drafts a class from a process description, validates it, previews it against live data | written; variables, formulas and the store shipped; statechart shipped; rules and actions shipped; access shipped |
| Operator | [`fadodb-operate/`](fadodb-operate/SKILL.md) | creates, sets and transitions documents on behalf of a person or a system, as a principal, with a key and a preview | Go API; the commands through its `Exec`; over HTTP with a key, from `fadodb serve` or from a host program that mounts the door, `fado/httpdoor`; from a shell with `fadodb exec`; as tools of the Model Context Protocol with `fadodb mcp` |
| Simulator | [`fadodb-simulate/`](fadodb-simulate/SKILL.md) | replays a scenario in a scratch database and keeps the class that worked | scratch files and `At` work; replay is by hand |
| Analyst | [`fadodb-mine/`](fadodb-mine/SKILL.md) | mines the event log and the views in SQL and feeds the finding back into the class | filter and read-only SQL shipped; `Events` on the Go API; DuckDB views designed |
| Its own principal | [`fadodb-own-work/`](fadodb-own-work/SKILL.md) | runs the agent's own work as documents: the task ledger, the approval inbox, the saga | principal, the rules of a class and per-transition rights enforced; acting for a person checked against a substitution; a rule calls a Go function of the host program, or the agent's endpoint over HTTP |

The person who embeds the engine in a Go program is a sixth caller, a
developer or a coding agent. That is an embedding guide, planned for
Phase 9, not a hat.

## Layout

```
skills/
  README.md               this page
  fadodb-design/SKILL.md  one folder per hat; SKILL.md is what the runtime loads
  fadodb-operate/SKILL.md
  fadodb-simulate/SKILL.md
  fadodb-mine/SKILL.md
  fadodb-own-work/SKILL.md
  reference/              the shared reference set, generated from the repository
examples/classes/         the sample classes the skills draw on, checked in CI
```

## Installing a skill

Copy a skill's folder into the place your runtime reads skills from; for
Claude Code that is `.claude/skills/` in the project or `~/.claude/skills/`
for every project. The folder name is the skill's name. A skill refers to
the repository's documents by path inside a clone and by their GitHub URL
otherwise.

## The rule these pages follow

Nothing in a skill describes as working what does not. Each section
carries one of the project's marks, shipped, in progress or designed, the
same marks the README, the ROADMAP and fadodb.org use, so an agent does
not write a rule on a schedule expecting it to fire before the phase
that builds timers. A pull request that changes what works moves these
marks in the same change; CLAUDE.md and CONTRIBUTING.md say so. Every class under
`examples/classes/` is validated and defined by `TestExampleClasses` in
`fado/` on every push, so an engine change that breaks a sample fails CI
before a reader meets it.

## Planned, in order

1. Done: `fadodb class check FILE` and `fadodb action check FILE`, so
   an agent checks a draft without writing Go and loops until the tool
   ends with 0. The designer's skill uses it in its step "Validate".
   Done as well: `fadodb exec`, which sends any command to a server
   from a shell, and `fadodb file`, which puts a file on a document
   and reads one, so an agent with a terminal needs no program of its
   own. The operator's skill says how.
2. The reference set, generated by a script from
   `internal/formula/catalog.json` and the decision records, so it
   cannot drift from the engine.
3. The designer skill written in full. The other four presuppose a class.
4. The first six samples, the cards the front page of fadodb.org links to,
   then one per shape of the catalogue.
5. The operator skill grows its command-set and MCP sections with Phase
   6: it has the commands over HTTP, from a shell and as tools of
   `fadodb mcp`.
