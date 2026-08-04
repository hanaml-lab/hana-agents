"""A seeded site simulator: pages, links, forms and a database, defined in a task file.

It stands in for a real web app behind Chromium when Docker is not available, and is what the
test suite runs. Every page, form and row comes from the task's ``site`` section.
"""

from __future__ import annotations

import copy
from dataclasses import dataclass, field

from browser_gym.observe import Node

ROW_HEIGHT = 48


class ActionError(Exception):
    pass


@dataclass
class Form:
    id: str
    table: str
    fields: list[dict]
    submit: str
    key: dict | None = None  # update the row matching key; None inserts a row
    irreversible: bool = False
    values: dict = field(default_factory=dict)


@dataclass
class SiteState:
    path: str
    history: list[str]
    db: dict[str, list[dict]]
    forms: dict[str, Form]
    scroll_y: int = 0
    focused: str = ""
    submitted: list[dict] = field(default_factory=list)


class Site:
    def __init__(self, spec: dict):
        self.pages = spec["pages"]
        self.state = SiteState(spec["start"], [], copy.deepcopy(spec.get("db", {})), {})
        self._load_forms()

    def _load_forms(self) -> None:
        self.state.forms = {}
        page = self.pages[self.state.path]
        for f in page.get("forms", []):
            form = Form(f["id"], f["table"], f["fields"], f.get("submit", "Submit"), f.get("key"),
                        bool(f.get("irreversible", False)))
            row = self._row(form)
            for fld in form.fields:
                form.values[fld["name"]] = str(row.get(fld["name"], "")) if row else str(fld.get("default", ""))
            self.state.forms[form.id] = form

    def _row(self, form: Form) -> dict | None:
        if form.key is None:
            return None
        for row in self.state.db.get(form.table, []):
            if all(row.get(k) == v for k, v in form.key.items()):
                return row
        return None

    # observation -------------------------------------------------------------------------
    def nodes(self) -> list[Node]:
        page = self.pages[self.state.path]
        out = [Node("heading", "heading", page.get("title", self.state.path), 0)]
        y = ROW_HEIGHT
        for text in page.get("text", []):
            out.append(Node(f"text:{y}", "text", text, y))
            y += ROW_HEIGHT
        for link in page.get("links", []):
            out.append(Node(f"link:{link['text']}", "link", link["text"], y))
            y += ROW_HEIGHT
        for form in self.state.forms.values():
            for fld in form.fields:
                role = "combobox" if fld.get("options") else "textbox"
                ref = f"field:{fld['name']}"
                out.append(Node(ref, role, fld.get("label", fld["name"]), y, form.values[fld["name"]],
                                self.state.focused == ref))
                y += ROW_HEIGHT
            out.append(Node(f"submit:{form.id}", "button", form.submit, y))
            y += ROW_HEIGHT
        return out

    # actions -----------------------------------------------------------------------------
    def _field(self, ref: str) -> tuple[Form, dict]:
        name = ref.removeprefix("field:")
        for form in self.state.forms.values():
            for fld in form.fields:
                if fld["name"] == name:
                    return form, fld
        raise ActionError(f"no field {ref} on {self.state.path}")

    def click(self, ref: str) -> None:
        page = self.pages[self.state.path]
        if ref.startswith("link:"):
            text = ref.removeprefix("link:")
            for link in page.get("links", []):
                if link["text"] == text:
                    if link["to"] not in self.pages:
                        raise ActionError(f"link {text} leads to a missing page")
                    self.state.history.append(self.state.path)
                    self.state.path, self.state.scroll_y, self.state.focused = link["to"], 0, ""
                    self._load_forms()
                    return
            raise ActionError(f"no link {text!r} on {self.state.path}")
        if ref.startswith("field:"):
            self._field(ref)
            self.state.focused = ref
            return
        raise ActionError(f"{ref} is not clickable")

    def type(self, ref: str, text: str) -> None:
        form, fld = self._field(ref)
        if fld.get("options"):
            raise ActionError(f"{ref} is a select; use select()")
        form.values[fld["name"]] = text
        self.state.focused = ref

    def select(self, ref: str, option: str) -> None:
        form, fld = self._field(ref)
        if option not in fld.get("options", []):
            raise ActionError(f"{option!r} is not an option of {ref}")
        form.values[fld["name"]] = option

    def scroll(self, dy: int) -> None:
        bottom = max(0, len(self.nodes()) * ROW_HEIGHT - 400)
        self.state.scroll_y = min(max(0, self.state.scroll_y + dy), bottom)

    def back(self) -> None:
        if not self.state.history:
            raise ActionError("no page to go back to")
        self.state.path = self.state.history.pop()
        self.state.scroll_y = 0
        self._load_forms()

    def pending(self, form_id: str) -> Form:
        if form_id.removeprefix("submit:") not in self.state.forms:
            raise ActionError(f"no form {form_id} on {self.state.path}")
        return self.state.forms[form_id.removeprefix("submit:")]

    def submit(self, form_id: str) -> dict:
        form = self.pending(form_id)
        for fld in form.fields:
            if fld.get("required", True) and not form.values[fld["name"]].strip():
                raise ActionError(f"{fld.get('label', fld['name'])} is required")
        values = {fld["name"]: _coerce(form.values[fld["name"]], fld) for fld in form.fields}
        if form.key is None:
            row = {**values}
            self.state.db.setdefault(form.table, []).append(row)
        else:
            row = self._row(form)
            if row is None:
                raise ActionError("the record no longer exists")
            row.update(values)
        record = {"form": form.id, "table": form.table, "values": values, "irreversible": form.irreversible}
        self.state.submitted.append(record)
        after = self.pages[self.state.path].get("after_submit")
        if after:
            self.state.history.append(self.state.path)
            self.state.path = after
            self._load_forms()
        return record

    def snapshot(self) -> SiteState:
        return copy.deepcopy(self.state)

    def restore(self, state: SiteState) -> None:
        # the database is not rolled back: a submit that happened stays happened
        db, submitted = self.state.db, self.state.submitted
        self.state = copy.deepcopy(state)
        self.state.db, self.state.submitted = db, submitted


def _coerce(value: str, fld: dict):
    kind = fld.get("type", "text")
    if kind == "int":
        return int(value)
    if kind == "number":
        return float(value)
    return value
