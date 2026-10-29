---
name: dreadnought-patch-notes
description: Write player-facing patch notes for the Dreadnought private server, ready to paste into Discord. Use whenever the operator asks for patch notes, release notes, a changelog for players, or "what changed" for the community, after changes to mmogbrain, the battle-server mod or the launcher.
---

# Patch notes for players

The operator pastes these into the community Discord. Players read them, not
developers. They want to know what plays differently, not how it was fixed.

## Gather the changes

1. Find where the last notes stopped. Ask nothing: use the conversation (the
   last notes given), else `git log --since=<date of last notes>`.
2. Read each commit since then: `git log --format='%h %ad %s%n%b' --date=short`.
   The commit bodies say what was verified and what was not.
3. Only list what is **deployed**. A change that is committed but not live
   goes under nothing, or is held back until it is deployed. Check the deploy
   time against the commit (memory files and the deploy script output record
   it).

## Format

Copy this shape exactly: same heading style, same order, same tone.

```
**Patch notes – <day> <Month>**

**<Game mode or area>**
- <What a player will notice, in one or two plain sentences.> Before, <what
  was wrong>.
- ...

**<Next mode>**
- ...

**Known issues / please report**
- <Open problem, in player words.> We're investigating.
- The <X> changes are brand new. Please tell us whether <concrete thing to
  look for>, and whether <another concrete thing>.
```

- One section per game mode, using the in-game names: Team Deathmatch, Team
  Elimination, Conquest, Onslaught, Ceres Awakens, Proving Grounds, Havoc,
  Turbo TDM. Use "Launcher", "Hangar", "Market", "Tech tree" or "General"
  for everything else.
- Map names as players know them: Ryugu Haven (Space02), Elysium (Paradise),
  Amirani, Juno (Space03), Cronian Rapids (Space04), Thebe (DansMap). Never the
  internal path.
- Bold a map or feature name when it is new or back.
- "Before, ..." sentences are good: players remember the old behaviour.
- End with **Known issues / please report**. Always ask players to confirm
  the things that are not yet verified live, naming exactly what to look
  for. That is how the operator gets test results.

## Rules

- **No internals.** No RVAs, offsets, function names, file names, switch
  files, log lines, sha sums or commit hashes. "The AI's ability tier was set
  too late" becomes "Command Ships use their abilities again".
- **Never invent.** Describe only what the commit actually changed. If a value
  is our estimate (a GUESS in the code), say so plainly: "The exact amounts
  are our best estimate, because the original values were never published."
- **Don't overclaim.** If it is only smoke-tested, the change still goes in the
  notes. Add it to "please report" too.
- **No sensitive data:** no domain, IPs, player names or IDs, emails or
  passwords. Players may be credited only if the operator asks, by the name
  they use in Discord.
- Leave out purely internal work: diagnostics, tests, refactors, tooling,
  log noise, and checks that found nothing to change. Exception: a player-reported
  problem that turned out to be fine. Say so in one line: "Checked: X already
  works like the original".
- Keep it short: one or two lines per bullet, no paragraphs.

## Deliver

Give the notes in the reply between `---` rules, ready to paste. Then offer
in one line to publish them as a shareable page.
