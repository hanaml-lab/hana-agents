import pytest

from browser_gym import Env, agents
from browser_gym.actions import Action, parse
from browser_gym.env import TASKS
from browser_gym.observe import Node, render

ALL = sorted(str(p.relative_to(TASKS)) for p in TASKS.rglob("*.yaml"))


def run(env, agent, seed=0):
    obs = env.reset(seed=seed)
    while not env.done:
        obs = env.step(agent.act(obs))
    return env.score()


@pytest.mark.parametrize("task", ALL)
def test_reference_agent_solves_every_task(task):
    env = Env(task)
    s = run(env, agents.Heuristic(env=env))
    assert s.success, [r.__dict__ for r in s.trace]
    assert s.irreversible_errors == 0


def test_parse_actions():
    assert parse('type(field:city, "Leeds")') == Action("type", "field:city", "Leeds")
    assert parse("scroll(400)").dy == 400
    assert parse('{"kind": "submit", "target": "submit:billing"}').kind == "submit"
    with pytest.raises(ValueError):
        parse("teleport(home)")


def test_wrong_irreversible_submit_is_counted_without_verifier():
    env = Env("forms/change-billing-address")
    s = run(env, agents.Heuristic(mistakes={"city": "York"}, env=env))
    assert not s.success and s.irreversible_errors == 1


def test_verifier_holds_the_submit_and_agent_corrects():
    env = Env("forms/change-billing-address", verifier=True)
    s = run(env, agents.Heuristic(mistakes={"city": "York"}, env=env))
    assert s.success and s.irreversible_errors == 0
    assert any("city" in r.blocked for r in s.trace)


def test_scoring_is_step_by_step():
    env = Env("forms/change-billing-address")
    s = run(env, agents.Heuristic(env=env))
    progress = [r.progress for r in s.trace]
    assert progress == sorted(progress) and progress[-1] == 1.0
    assert progress[0] == 0.0


def test_backtrack_restores_last_good_state_but_not_the_database():
    env = Env("admin/deactivate-user")
    env.reset()
    env.step(Action("click", "link:r.okafor"))
    env.step(Action("select", "field:status", "inactive"))
    env.step(Action("click", "link:nowhere"))  # fails: no such link
    env.step(Action("back"))  # leaves the form, losing the unsaved selection
    assert env.site.state.path == "/admin/users"
    env.backtrack()
    assert env.site.state.path == "/admin/users/r.okafor"
    assert env.site.state.forms["user"].values["status"] == "inactive"


def test_offscreen_nodes_dropped_before_truncation():
    nodes = [Node(f"n{i}", "link", f"item {i}", i * 100) for i in range(40)]
    nodes.append(Node("f", "textbox", "Search", 50, focused=True))
    out = render(nodes, scroll_y=0, viewport=500, budget=1000)
    lines = out.splitlines()
    assert lines[0].startswith("[f]")
    assert "item 4" in out and "item 5" not in out
    assert "35 nodes off screen" in out
