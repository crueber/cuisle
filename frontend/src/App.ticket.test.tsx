// WP-T9 tests: the ticket modal is mounted by the App shell over the standup screen. Opening, stepping and closing go through
// state/ticket.ts; leaving the standup screen closes it; opening makes exactly one facade call (ticket.get).
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { setApi, wailsApi } from "./api";
import { createMockApi, type MockApi } from "./api/mock";
import { FIXTURE_VIEWS } from "./api/mock-fixtures";
import { standups } from "./state/app";
import { goConfigure, route } from "./state/route";
import { views } from "./state/standup";
import { closeTicket, resetTicketState, ticketSession } from "./state/ticket";
import { openTicketFromRow } from "./state/ticketSteps";

vi.mock("./views/standup/StandupScreen", async () => {
  const { h } = await import("preact");
  return { StandupScreen: () => h("div", { "data-testid": "standup" }, "standup-double") };
});
vi.mock("./views/configure/ConfigureScreen", async () => {
  const { h } = await import("preact");
  return { ConfigureScreen: () => h("div", { "data-testid": "configure" }, "configure-double") };
});

let mock: MockApi;
beforeEach(async () => {
  mock = createMockApi();
  setApi(mock);
  const boot = await mock.app.bootstrap();
  standups.value = boot.standups;
  const mob = FIXTURE_VIEWS[0]!;
  views.value = new Map([[mob.standupId, mob]]);
  route.value = { view: "standup", standupId: mob.standupId, person: "iris" };
  mock.resetCalls();
});
afterEach(() => {
  cleanup();
  closeTicket();
  resetTicketState();
  setApi(wailsApi);
});

describe("App mounts the ticket modal", () => {
  it("renders nothing until a row opens a ticket, then the dialog for it with one ticket.get", async () => {
    render(<App />);
    expect(screen.queryByRole("dialog")).toBeNull();
    act(() => openTicketFromRow("MOB-1421"));
    expect(screen.getByRole("dialog", { name: /Ticket MOB-1421/ })).toBeTruthy();
    await waitFor(() => expect(mock.calls.map((c) => c.method)).toContain("ticket.get"));
    expect(mock.calls.map((c) => c.method).filter((m) => m !== "ticket.get")).toEqual([]);
  });

  it("J steps to the next ticket of the person and K back; the global keys stay out of it", async () => {
    render(<App />);
    act(() => openTicketFromRow("MOB-1421"));
    const first = ticketSession.value!.issueId;
    await waitFor(() => expect(screen.getByRole("dialog")).toBeTruthy());
    const dlg = screen.getByRole("dialog");
    fireEvent.keyDown(dlg, { key: "j" });
    await waitFor(() => expect(ticketSession.value!.issueId).not.toBe(first));
    fireEvent.keyDown(dlg, { key: "k" });
    await waitFor(() => expect(ticketSession.value!.issueId).toBe(first));
    expect(mock.calls.some((c) => c.method === "sync.start")).toBe(false);
  });

  it("Escape closes it", async () => {
    render(<App />);
    act(() => openTicketFromRow("MOB-1421"));
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(ticketSession.value).toBeNull();
  });

  it("leaving the standup screen closes it", async () => {
    render(<App />);
    act(() => openTicketFromRow("MOB-1421"));
    expect(screen.getByRole("dialog")).toBeTruthy();
    act(() => goConfigure(null));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(ticketSession.value).toBeNull();
  });

  it("a comment that could not be sent says Not sent once, keeps the text and offers Retry / Edit / Discard", async () => {
    mock = createMockApi({ failures: { "ticket.addComment": { code: "offline", message: "couldn't reach Jira (offline)", times: 1 } } });
    setApi(mock);
    render(<App />);
    act(() => openTicketFromRow("MOB-1421"));
    const box = (await screen.findByRole("textbox", { name: "Comment" })) as HTMLTextAreaElement;
    fireEvent.input(box, { target: { value: "will not reach Jira" } });
    fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(screen.getByText(/couldn't reach Jira/)).toBeTruthy());
    expect(screen.getByText(/couldn't reach Jira/).textContent).toBe("Not sent · couldn't reach Jira (offline)");
    expect(screen.getByText("Your text is kept.")).toBeTruthy();
    for (const name of ["Retry", "Edit", "Discard"]) expect(screen.getByRole("button", { name })).toBeTruthy();
  });
});
