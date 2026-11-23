// App shell (orchestrator-owned): renders by route. The standup screen stays mounted (display:none) while Configure is
// open so Done returns instantly with scroll intact (architecture §4.5).
import { useEffect } from "preact/hooks";
import { standups } from "./state/app";
import { route } from "./state/route";
import { currentView } from "./state/standup";
import { banners, toasts, dismissBanner, dismissToast, keyLegendOpen, runToastAction, switcherOpen } from "./state/ui";
import { StandupScreen } from "./views/standup/StandupScreen";
import { ConfigureScreen } from "./views/configure/ConfigureScreen";
import { ConnectScreen } from "./views/connect/ConnectScreen";
import { ReconnectScreen } from "./views/connect/ReconnectScreen";
import { Banner } from "./components/Banner";
import { KeyLegend } from "./components/KeyLegend";
import { Toast } from "./components/Toast";

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

  // Popovers never outlive the screen they were opened on.
  useEffect(() => {
    keyLegendOpen.value = false;
    switcherOpen.value = false;
  }, [r]);

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
      <KeyLegend
        open={legendOpen && r.view === "standup"}
        onClose={() => {
          keyLegendOpen.value = false;
        }}
        showCommits={options?.showCommits ?? false}
        showPRs={options?.showPRs ?? false}
        showComments={options?.showComments ?? false}
        devActivity={options?.devActivity ?? "unknown"}
      />
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
