"""
No fire-and-forget task may be left unreferenced.

asyncio holds only a WEAK reference to a task — "Save a reference to the
result of this function, to avoid a task disappearing mid-execution", from
`asyncio.create_task`'s own documentation — so a task nothing else holds can
be collected part-way through.

The failure is silent and load-dependent, which is the worst combination
this tree deals with: the work simply does not finish, with no exception and
no log line. The consequences are not uniform either. Losing the wake word
listener's restart leaves a device permanently deaf; losing an OTA leaves it
half-updated; losing a media command leaves HA's entity showing something
the speaker is not doing.

This is an AST guard rather than a grep, because the thing that makes a call
safe is what happens to its RESULT, which no regex can see.
"""

import ast
from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]

# Every module that starts background work. Listed rather than globbed: a
# new module that spawns tasks should have to be added here deliberately,
# which is the moment to think about who holds its references.
MODULES = (
    "em_controller.py", "em_api.py", "em_esphome.py",
    "em_player.py", "em_ns.py", "em_tap_burst.py",
)


def _unreferenced_create_tasks(path: Path):
    """
    Lines where `create_task(...)` is a bare expression statement — the
    result assigned to nothing, chained to nothing, awaited by nothing.

    A call whose result is assigned, returned, awaited, or has a
    done-callback chained onto it is fine: something holds it.
    """
    tree = ast.parse(path.read_text())
    for parent in ast.walk(tree):
        for child in ast.iter_child_nodes(parent):
            child.parent = parent

    out = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        func = node.func
        name = func.attr if isinstance(func, ast.Attribute) else getattr(func, "id", None)
        if name != "create_task":
            continue
        if isinstance(node.parent, ast.Expr):
            out.append(node.lineno)
    return out


def test_no_task_is_started_without_something_holding_it():
    strays = {
        module: lines
        for module in MODULES
        if (lines := _unreferenced_create_tasks(CONTROLLER / module))
    }
    assert not strays, (
        "these tasks can be garbage-collected mid-execution — start them "
        f"through the module's _spawn helper instead: {strays}"
    )


def test_the_spawn_helpers_hold_a_reference_and_report_failures():
    """
    Holding the task is half of it. A task nobody awaits also swallows its
    exception until some later collection surfaces it as "Task exception
    was never retrieved", by which point the context is gone.
    """
    for module in ("em_controller.py", "em_api.py"):
        src = (CONTROLLER / module).read_text()
        assert "_background_tasks" in src, \
            f"{module} needs somewhere to hold its tasks"
        body = src[src.index("def _spawn("):]
        body = body[:body.index("\n\n\n")] if "\n\n\n" in body else body
        assert "_background_tasks.add" in body, \
            f"{module}: _spawn must hold the task"
        assert "_background_tasks.discard" in body, \
            f"{module}: _spawn must release it again, or the set is a leak"
        assert "add_done_callback" in body and "exception" in body, \
            f"{module}: _spawn must surface a failure"


def test_the_satellite_helper_does_the_same():
    """
    em_esphome's lives on the satellite rather than at module level: its
    message handlers are plain generators and cannot await, and the tasks
    they start belong to one connection.
    """
    src  = (CONTROLLER / "em_esphome.py").read_text()
    body = src[src.index("def _spawn(coro"):]
    body = body[:body.index("self._spawn = _spawn")]
    assert "self._timer_tasks.add(task)" in body, \
        "the satellite's _spawn must hold the task"
    assert "self._timer_tasks.discard" in body, \
        "and release it, or the set grows for the life of the connection"
    assert "exception()" in body, "and report a failure"
