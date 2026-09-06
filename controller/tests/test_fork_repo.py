"""
Which repository this build takes its updates from.

`github_repo` drives both update paths — the OTA poller reads that
repository's releases for device firmware, and the dashboard's controller
notice reads its `controller-v*` tags. A fresh database gets the fork from
migration v1; a database copied across from an upstream install already ran
v1 and stored upstream's name, and the recommended switch-over is exactly
that copy (the CA has to come with it or no fielded device connects).

Left uncorrected, the dashboard offers upstream firmware to devices running
this tree's and reports "up to date" while a fork release exists.
"""

import sqlite3

import em_db


def _fresh(tmp_path):
    p = str(tmp_path / "em.db")
    em_db.init(p)
    return p


def test_a_fresh_database_names_the_fork(tmp_path):
    _fresh(tmp_path)
    assert em_db.get_config("github_repo") == em_db.FORK_REPO


def test_a_database_inherited_from_upstream_is_repointed(tmp_path):
    p = str(tmp_path / "em.db")
    c = sqlite3.connect(p)
    for sql in em_db.MIGRATIONS:
        c.executescript(sql)
    c.execute("UPDATE system_config SET value = ? WHERE key = 'github_repo'",
              (em_db.UPSTREAM_REPO,))
    c.execute("UPDATE system_config SET value = 'v2.13.0' WHERE key = 'latest_version'")
    c.commit()
    c.close()

    em_db.init(p)
    assert em_db.get_config("github_repo") == em_db.FORK_REPO
    # The cached release describes upstream's releases, so it goes with the
    # name rather than being shown until the next poll happens to land.
    assert em_db.get_config("latest_version") is None


def test_a_deliberate_choice_survives_a_restart(tmp_path):
    """
    The correction runs ONCE. Somebody who genuinely wants upstream's
    firmware must be able to say so and have it stick — re-adopting on every
    startup is the same silent refusal, from the other side.
    """
    p = _fresh(tmp_path)
    em_db.set_config("github_repo", em_db.UPSTREAM_REPO)
    em_db.init(p)
    assert em_db.get_config("github_repo") == em_db.UPSTREAM_REPO


def test_the_decision_is_pure_and_covers_the_three_cases():
    assert em_db.repo_to_adopt(em_db.UPSTREAM_REPO, False) == em_db.FORK_REPO
    assert em_db.repo_to_adopt(None, False) == em_db.FORK_REPO
    assert em_db.repo_to_adopt(em_db.UPSTREAM_REPO, True) is None
    assert em_db.repo_to_adopt("someone/else", False) is None
    # Even when the choice IS upstream, having been made it is left alone.
    assert em_db.repo_to_adopt(em_db.UPSTREAM_REPO, True) is None
