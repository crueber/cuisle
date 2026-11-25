// WP-38F: validation messages from the save queue reach the Configure controls (key `${standupId}:${field}`).
import { cleanup, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { AppError } from "../../api/errors";
import type { StandupConfig } from "../../api/types";
import { fieldErrors } from "../../state/save";
import { FieldError } from "./FieldError";
import { PulseSection } from "./sections/Pulse";
import { SourceSection } from "./sections/Source";
import { WholeBoardSection } from "./sections/WholeBoard";

const cfg = {
  id: 7, name: "Svc", space: { id: "1", key: "S", name: "S" }, board: { id: "2", name: "B", type: "scrum" }, scope: "sprint", extraFilter: "labels = x",
  filterCheck: null, draft: false, backlogRows: 12, pulseDays: 21, showCommits: true, showPRs: true, showComments: true, shadeWeekends: true,
  doneWindow: "sprint", columns: [], speakers: [],
  lanes: [{ lane: "prog", label: "In progress", defaultLabel: "In Progress", kind: "working", custom: false, hue: 0, limitDays: 5, columns: [] }, { lane: "done", label: "Done", defaultLabel: "Done", kind: "done", custom: false, hue: 0, limitDays: 0, columns: [] }],
} as unknown as StandupConfig;

const reject = (key: string, message: string) => {
  fieldErrors.value = new Map([[key, new AppError("validation", message, "", false, "x")]]);
};

beforeEach(() => {
  fieldErrors.value = new Map();
});
afterEach(() => cleanup());

describe("validation messages", () => {
  it("FieldError renders nothing without an error and the message with one", () => {
    const { container, rerender } = render(<FieldError standupId={7} field="backlogRows" />);
    expect(container.textContent).toBe("");
    reject("7:backlogRows", "Backlog rows must be 0 to 50.");
    rerender(<FieldError standupId={7} field="backlogRows" />);
    expect(screen.getByRole("alert").textContent).toBe("Backlog rows must be 0 to 50.");
  });

  it("only the matching standup and field", () => {
    reject("8:backlogRows", "other standup");
    render(<FieldError standupId={7} field="backlogRows" />);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("Source: Name and Extra filter show their message and are aria-invalid", () => {
    reject("7:name", "Name is too long.");
    const { rerender } = render(<SourceSection config={cfg} columns={3} />);
    expect(screen.getByText("Name is too long.")).toBeTruthy();
    expect(screen.getByLabelText("Name").getAttribute("aria-invalid")).toBe("true");
    reject("7:extraFilter", "Filter is too long.");
    rerender(<SourceSection config={cfg} columns={3} />);
    expect(screen.getByText("Filter is too long.")).toBeTruthy();
    expect(screen.getByLabelText("Extra filter").getAttribute("aria-invalid")).toBe("true");
  });

  it("Whole board: the Backlog rows stepper shows its message", () => {
    reject("7:backlogRows", "Backlog rows must be 0 to 50.");
    render(<WholeBoardSection config={cfg} columns={3} />);
    expect(screen.getByText("Backlog rows must be 0 to 50.")).toBeTruthy();
  });

  it("Pulse: a status limit shows its message", () => {
    reject("7:limit:prog", "Limit must be 0 to 99.");
    render(<PulseSection config={cfg} columns={3} />);
    expect(screen.getByText("Limit must be 0 to 99.")).toBeTruthy();
  });
});
