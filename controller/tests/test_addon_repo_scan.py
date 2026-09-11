"""
Nothing in this repository may look like an add-on config unless it is one.

Home Assistant's Supervisor scans an add-on repository with `**/config.*` and
parses every hit as an add-on configuration. A file that merely SHARES that
name is read, fails, and lands in every user's Supervisor log:

    ERROR [supervisor.utils.yaml] Can't read YAML file
      /data/apps/git/46aaf331/oww_forge/config.template.yml
      - while scanning for the next token, line 17, column 1
    WARNING [supervisor.store.data] Can't read ... from repository 46aaf331

`oww_forge/config.template.yml` was exactly that: a TRAINING config template
whose line 17 is the placeholder `@PHRASES@`, which is not YAML and is not
meant to be. It was reported on every store scan for days, on this fork and
upstream alike, while the store sat a release behind what the default branch
said.

The glob is Supervisor's and cannot be changed from here, so the rule is
ours: a file matching `config.*` in this repository is an add-on config and
parses, or it is named something else.
"""

import pathlib

import pytest

yaml = pytest.importorskip("yaml")

ROOT = pathlib.Path(__file__).resolve().parents[2]

# The real add-on configs. Everything else matching the glob is a mistake.
ADDON_CONFIGS = {"controller/config.yaml", "controller-ea/config.yaml"}

# Supervisor globs `config.*`; only these extensions are parsed as YAML/JSON.
PARSED_SUFFIXES = {".yaml", ".yml", ".json"}


def _candidates():
    for p in ROOT.glob("**/config.*"):
        if ".git" in p.parts or not p.is_file():
            continue
        if p.suffix not in PARSED_SUFFIXES:
            continue          # config.go and friends are never parsed
        yield p


def test_only_real_add_on_configs_match_supervisors_glob():
    found = {str(p.relative_to(ROOT)) for p in _candidates()}
    extra = found - ADDON_CONFIGS
    assert not extra, (
        f"these are read by Supervisor as add-on configs and are not one: "
        f"{sorted(extra)}. Rename them so they do not match `config.*` — a "
        f"template or example that cannot parse becomes an error in every "
        f"user's Supervisor log, on every scan.")


def test_the_add_on_configs_themselves_parse():
    for rel in sorted(ADDON_CONFIGS):
        path = ROOT / rel
        assert path.is_file(), f"{rel} is missing"
        with path.open(encoding="utf-8") as fh:
            data = yaml.safe_load(fh)
        assert isinstance(data, dict), f"{rel} is not a mapping"
        assert data.get("version"), f"{rel} has no version pin"
