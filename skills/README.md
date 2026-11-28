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

**Status on 2026-11-28: all five skills are written, with the marks of
what Phases 8 and 9 built and of the seal of the log, Phase 10's
first step.** Each tells the
agent what to read in the manual under `../docs/`, shows one piece of
work with the engine's own answers, and lists what goes wrong most
often. The engine defines the class each skill shows in a test on
each push, and for every skill the same test sends the commands
of the page and compares the answers. The shared reference set is the
manual: the class format, the formula language, the functions, the
commands and the refusals are chapters of it. The sample corpus under
`../examples/classes/` holds four classes.

## The five hats

An agent wears one of five hats when it works with FadoDB. One skill each,
so the right one loads for the task at hand.

| Hat | Skill | What it does | Today |
|---|---|---|---|
| Designer | [`fadodb-design/`](fadodb-design/SKILL.md) | drafts a class from a process description, validates it, previews it against live data | written; variables, formulas and the store shipped; statechart shipped; rules and actions shipped; access shipped |
| Operator | [`fadodb-operate/`](fadodb-operate/SKILL.md) | creates, sets and transitions documents on behalf of a person or a system, as a principal, with a key and a preview | Go API; the commands through its `Exec`; over HTTP with a key, from `fadodb serve` or from a host program that mounts the door, `fado/httpdoor`; from a shell with `fadodb exec`; as tools of the Model Context Protocol with `fadodb mcp` |
| Simulator | [`fadodb-simulate/`](fadodb-simulate/SKILL.md) | replays a scenario in a scratch database and keeps the class that worked | scratch files and `At` work; replay is by hand |
| Analyst | [`fadodb-mine/`](fadodb-mine/SKILL.md) | mines the event log and the views, with the engine's commands and in SQL, and feeds the finding back into the class | filter, read-only SQL and `flow` shipped; `events` as a command and in Go; the spread of the stays in `flow`, `throughput`, `paths`, the documents behind each of their numbers, `lineage`, `row_history`, and reading the file of a database with DuckDB from a program of one's own, shipped with Phase 8 |
| Its own principal | [`fadodb-own-work/`](fadodb-own-work/SKILL.md) | runs the agent's own work as documents: the task ledger, the approval inbox, the saga | principal, the rules of a class and per-transition rights enforced; acting for a person checked against a substitution; a rule calls a Go function of the host program, or the agent's endpoint over HTTP, or, through the library of C since Phase 9, a function of a program in any language, Python's among them (ADR-0102) |

The person who embeds the engine in a program is a seventh caller, a
developer or a coding agent. That is not a hat but a guide of the
manual: in Go, [opening a database](../docs/manual/opening-a-database.md)
and [naming the caller](../docs/manual/naming-the-caller.md); in any
other language, through the library of C,
[in a program of another language](../docs/manual/other-languages.md)
(Phase 9).

What people see of a server, its faces and their look, is not the
engine's. The skill that builds faces belongs to the tool that builds
them, and left this folder on the owner's word on 2026-11-27
(ADR-0060).

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
not write a rule on a schedule expecting it to fire: that is not built,
and the engine refuses it with `not_supported`. A pull request that changes what works moves these
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
2. Done, as the manual: the reference set is its chapters, and
   `make docs` writes the lists of functions, commands and refusals
   from the engine's own tables, so they cannot drift from the engine;
   a test fails when they fall behind.
3. Done: the designer skill is written in full. The other four
   presuppose a class.
4. The first six samples, the cards the front page of fadodb.org links to,
   then one per shape of the catalogue.
5. Done with Phase 6: the operator skill has the commands over HTTP,
   from a shell and as tools of `fadodb mcp`.
