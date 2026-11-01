#!/usr/bin/env bash
# Regenerate status.json for the GitHub Pages landing site.
# Called by .github/workflows/update-site-status.yml on a schedule and on pushes
# to master. Safe to run locally (uses the ambient `gh` auth).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

python3 - <<'PY'
import json, os, subprocess, sys, datetime

def git(*args):
    return subprocess.run(["git", *args], capture_output=True, text=True, check=True).stdout.strip()

remote = git("remote", "get-url", "origin")
name = remote.rstrip("/").rsplit("/", 1)[-1]
name = name[:-4] if name.endswith(".git") else name
owner = remote.rstrip("/").rsplit("/", 2)[-2]
repo_slug = f"{owner}/{name}"

def gh_json(path, jq=None):
    # `path` is already a full API path (e.g. "repos/<slug>" or "search/issues?...").
    cmd = ["gh", "api", path]
    if jq:
        cmd += ["--jq", jq]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        sys.stderr.write(f"gh api {path} failed: {r.stderr.strip()}\n")
        return None
    return json.loads(r.stdout) if r.stdout.strip() else None

repo = gh_json(f"repos/{repo_slug}", "{name_with_owner, stargazers_count, forks_count, pushed_at}") or {
    "name_with_owner": repo_slug, "stargazers_count": 0, "forks_count": 0, "pushed_at": "",
}

open_issues = gh_json(f"search/issues?q=repo:{repo_slug}+type:issue+state:open", ".total_count") or 0
open_prs = gh_json(f"search/issues?q=repo:{repo_slug}+type:pr+state:open", ".total_count") or 0
bug_items = gh_json(
    f"search/issues?q=repo:{repo_slug}+type:issue+state:open+label:bug&per_page=8",
    "[.items[] | {number, title, url: .html_url, labels: [.labels[].name]}]",
) or []

head_sha = git("log", "-1", "--format=%H")
head_date = git("log", "-1", "--format=%cI")
head_subject = git("log", "-1", "--format=%s")
commits_7d = int(git("rev-list", "--count", "--since=7 days ago", "HEAD") or 0)

prev = {}
if os.path.exists("status.json"):
    try:
        prev = json.load(open("status.json", encoding="utf-8"))
    except Exception:
        prev = {}

last_sha = prev.get("activity", {}).get("last_changes_sha")
range_spec = f"{last_sha}..HEAD" if last_sha else "HEAD"
try:
    log_fmt = "%H%x1f%cI%x1f%s"
    raw = git("log", f"--format={log_fmt}", range_spec)
    rows = [line.split("\x1f") for line in raw.splitlines() if line.strip()]
except subprocess.CalledProcessError:
    rows = [line.split("\x1f") for line in git("log", "-8", f"--format={log_fmt}").splitlines() if line.strip()]

AREA_MAP = [
    ("battle-server-mod", "Combat servers"),
    ("matchmaker", "Matchmaking"),
    ("mmogbrain", "Backend"),
    ("dn-launcher", "Launcher"),
    ("launcher", "Launcher"),
    ("contracts", "Contracts"),
    ("shipxp", "Progression"),
    ("conquest", "Conquest"),
    ("onslaught", "Onslaught"),
    ("havoc", "Havoc"),
    ("ceres", "Ceres Awakens"),
    ("legacy-api", "Profiles & inventory"),
    ("auth-server", "Accounts"),
    ("gateway", "Gateway"),
    ("game-manager", "Server hosting"),
    ("dn-dedicated", "Server hosting"),
    ("master-server", "Server browser"),
    ("admin-cli", "Operator tools"),
    ("skills", "General"),
    ("docs", "Docs"),
    ("scripts", "Tooling"),
    ("chore(site)", "Website"),
]

def area_of(subject):
    prefix = subject.split(":", 1)[0].strip().lower() if ":" in subject else ""
    for key, area in AREA_MAP:
        if prefix == key or prefix.startswith(key):
            return area
    return "General"

def player_subject(subject):
    if ":" in subject:
        return subject.split(":", 1)[1].strip()
    return subject

recent = [
    {
        "sha": sha,
        "short": sha[:7],
        "date": date,
        "area": area_of(subject),
        "subject": player_subject(subject),
        "url": f"https://github.com/{repo_slug}/commit/{sha}",
    }
    for sha, date, subject in rows
][:8]

status = {
    "generated_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "repo": {
        "name": name,
        "url": f"https://github.com/{repo_slug}",
        "stars": repo["stargazers_count"],
        "forks": repo["forks_count"],
        "open_issues": open_issues,
        "open_prs": open_prs,
    },
    "activity": {
        "last_commit_at": head_date,
        "last_commit_sha": head_sha,
        "last_commit_subject": head_subject,
        "last_changes_sha": head_sha,
        "commits_last_7d": commits_7d,
    },
    "recent_changes": recent,
    "known_issues": bug_items,
    "run_url": os.environ.get("GITHUB_RUN_URL", ""),
}

with open("status.json", "w", encoding="utf-8") as f:
    json.dump(status, f, indent=2, ensure_ascii=False)
    f.write("\n")
print(f"status.json written: {len(recent)} changes, {len(bug_items)} known issues, generated {status['generated_at']}")
PY
