// App shell (orchestrator-owned): renders by route. The standup screen stays mounted (display:none) while Configure is
// open so Done returns instantly with scroll intact (architecture §4.5).
import { useEffect } from "preact/hooks";
import { standups } from "./state/app";
import { paletteOpen } from "./state/palette";
import { route } from "./state/route";
import { currentView } from "./state/standup";
import { backTicket, closeTicket, openLinkedTicket, stepToTicket, ticketSession } from "./state/ticket";
import { banners, toasts, dismissBanner, dismissToast, keyLegendOpen, runToastAction, switcherOpen } from "./state/ui";
import { StandupScreen } from "./views/standup/StandupScreen";
import { TicketModal } from "./views/ticket/TicketModal";
import { useViewport } from "./views/useViewport";
import { ConfigureScreen } from "./views/configure/ConfigureScreen";
import { ConnectScreen } from "./views/connect/ConnectScreen";
import { ReconnectScreen } from "./views/connect/ReconnectScreen";
import { Banner } from "./components/Banner";
import { KeyLegend } from "./components/KeyLegend";
import { lazy } from "./components/lazy";
import { Toast } from "./components/Toast";

// The command palette (F14) is a lazy chunk, fetched the first time it opens: cold start stays flat.
const Palette = lazy(() => import("./views/palette/PaletteDialog").then((m) => m.PaletteDialog));

/** "/" (ux-spec §8): open the switcher menu when the tabs collapsed to it, else focus the first tab. One-shot. */
function openSwitcher(): void {
  const nav = document.querySelector(".sf-tabs");
  if (!nav) return;
  if (nav.getAttribute("data-mode") === "menu") {
    nav.querySelector<HTMLElement>(".cbx-menu-btn")?.click();
  } else {
    nav.querySelector<HTMLElement>(".sf-tab")?.focus();
  }
}

export function App() {
  const r = route.value;
  const options = currentView.value?.options;
  const legendOpen = keyLegendOpen.value;
  const wantSwitcher = switcherOpen.value;
  const ticket = ticketSession.value;
  const { vw, vh } = useViewport();

  // Popovers never outlive the screen they were opened on.
  useEffect(() => {
    keyLegendOpen.value = false;
    switcherOpen.value = false;
  }, [r]);

  // The ticket modal is an overlay of the standup screen: leaving that screen (Configure, Connect) closes it.
  useEffect(() => {
    if (r.view !== "standup" && ticketSession.peek() !== null) closeTicket();
  }, [r.view]);

  useEffect(() => {
    if (!wantSwitcher) return;
    switcherOpen.value = false;
    openSwitcher();
  }, [wantSwitcher]);

  return (
    <>
      <div class="banners" role="region" aria-label="Notifications">
        {banners.value.map((b) => (
          <Banner key={b.id} kind={b.kind} message={b.message} action={b.action} onDismiss={b.dismissible ? () => dismissBanner(b.id) : undefined} />
        ))}
      </div>
      {r.view === "connect" ? (
        standups.value.length > 0 ? <ReconnectScreen /> : <ConnectScreen />
      ) : (
        <>
          <div class="screen" style={{ display: r.view === "standup" ? undefined : "none" }}>
            <StandupScreen />
          </div>
          {r.view === "configure" && <ConfigureScreen />}
        </>
      )}
      {ticket && r.view === "standup" ? (
        <TicketModal
          standupId={ticket.standupId}
          issueId={ticket.issueId}
          steps={ticket.steps}
          stack={ticket.stack}
          vw={vw}
          vh={vh}
          onStep={stepToTicket}
          onOpenLinked={openLinkedTicket}
          onBack={() => void backTicket()}
          onClose={closeTicket}
        />
      ) : null}
      {paletteOpen.value !== null ? <Palette /> : null}
      {legendOpen && r.view === "standup" ? (
        <KeyLegend
          open
          onClose={() => {
            keyLegendOpen.value = false;
          }}
          showCommits={options?.showCommits ?? false}
          showPRs={options?.showPRs ?? false}
          showComments={options?.showComments ?? false}
          devActivity={options?.devActivity ?? "unknown"}
          lanes={currentView.value?.lanes}
        />
      ) : null}
      <div class="toasts" aria-live="polite">
        {toasts.value.map((t) => (
          <Toast
            key={t.id}
            message={t.message}
            action={t.action ? { label: t.action.label, run: () => runToastAction(t.id) } : undefined}
            onDismiss={() => dismissToast(t.id)}
          />
        ))}
      </div>
    </>
  );
}
