// Standup header (ux-spec §2.1): wordmark, standup tabs, the date standing alone in the middle, then the right cluster
// (Find, sync status, Resync, Update when a newer release is available (ADR 0016), Configure).
// What collapses with width comes from layout/responsive headerCollapse. Sync age uses Date.now() at render only.
import { Icon } from "../../components/Icon";
import { tabsAvailableWidth } from "../../layout/tabs";
import { headerCollapse } from "../../layout/responsive";
import { createStandup } from "../../state/config";
import { tabStandups } from "../../state/app";
import { goConfigure, route } from "../../state/route";
import { currentView, switchStandup } from "../../state/standup";
import { cancelSync, startSync, syncStatus } from "../../state/sync";
import { updateAvailable, updateDialogOpen, updateStatus } from "../../state/update";
import { findKeyLabel, openPalette, paletteOpen } from "../../state/palette";
import { Tabs } from "./Tabs";
import { UpdateDialog } from "./UpdateDialog";
import { WORDMARK_WIDTH, dateWidth, dayLabel, rightClusterWidth, syncLabel } from "./headerText";
import type { HeaderProps } from "./props";

export function Header(props: HeaderProps) {
  const { vw } = props;
  const r = route.value;
  const standupId = r.view === "standup" ? r.standupId : null;
  const view = currentView.value;
  const status = standupId === null ? undefined : syncStatus.value.get(standupId);
  const running = status?.state === "running";
  // A running sync always shows its progress text, however narrow the window (otherwise only ↻ → × would change).
  const base = headerCollapse(vw);
  const findKey = findKeyLabel();
  const collapse = running ? { ...base, syncLabel: true } : base;
  const update = updateStatus.value;
  const hasUpdate = updateAvailable(update);
  const availW = tabsAvailableWidth(vw, WORDMARK_WIDTH, rightClusterWidth(collapse, hasUpdate) + dateWidth(collapse));
  const label = syncLabel(status, view?.lastSyncedAt ?? 0, Date.now());
  const pct = running && status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : null;

  return (
    <header class="sf-header">
      <span class="sf-header__wordmark">CUISLE</span>
      <span class="sf-header__rule" aria-hidden="true" />
      <Tabs
        tabs={tabStandups()}
        activeId={standupId ?? 0}
        availW={availW}
        onSelect={(id) => {
          void switchStandup(id);
        }}
        onAdd={() => {
          void createStandup();
        }}
      />
      <span class="sf-header__spacer" />
      {collapse.date && view ? <span class="sf-header__date">{dayLabel(view.today)}</span> : null}
      <span class="sf-header__spacer" />
      {standupId === null ? null : (
        <div class="sf-header__sync">
          {collapse.syncLabel ? (
            <span
              class="sf-header__label"
              data-alert={label.alert ? "true" : "false"}
              data-running={running ? "true" : "false"}
              role="status"
            >
              {label.text}
            </span>
          ) : null}
          <button
            type="button"
            class="sf-header__configure sf-header__find"
            aria-label={`Find a ticket or run a command (${findKey})`}
        aria-expanded={paletteOpen.value !== null}
            onClick={() => openPalette()}
          >
            <Icon name="search" size={20} strokeWidth={2.2} />
            {collapse.configureLabel ? (
              <>
                <span>Find</span>
                <kbd class="sf-header__kbd" aria-hidden="true" data-key={findKey} />
              </>
            ) : null}
          </button>
          {running ? (
            <button
              type="button"
              class="sf-header__configure"
              aria-label="Cancel sync"
              title={label.text}
              onClick={() => {
                void cancelSync(standupId);
              }}
            >
              <Icon name="close" size={20} strokeWidth={2.2} />
              {collapse.configureLabel ? <span>Cancel</span> : null}
            </button>
          ) : (
            <button
              type="button"
              class="sf-header__configure"
              aria-label="Resync (R)"
              title={label.text}
              onClick={() => {
                void startSync(standupId, "incremental");
              }}
            >
              <Icon name="resync" size={20} strokeWidth={2.2} />
              {collapse.configureLabel ? <span>Resync</span> : null}
            </button>
          )}
        </div>
      )}
      {hasUpdate && update.available ? (
        <button
          type="button"
          class="sf-header__configure sf-header__update"
          aria-label={`Update to ${update.available.version}`}
          title={`Cuisle ${update.available.version} is available`}
          onClick={() => {
            updateDialogOpen.value = true;
          }}
        >
          <Icon name="download" size={20} strokeWidth={2.2} />
          {collapse.configureLabel ? <span>Update</span> : null}
        </button>
      ) : null}
      <button type="button" class="sf-header__configure" aria-label="Configure" onClick={() => goConfigure(null)}>
        <Icon name="sliders" size={20} strokeWidth={2.2} />
        {collapse.configureLabel ? <span>Configure</span> : null}
      </button>
      {running ? (
        <div
          class="sf-header__progress"
          role="progressbar"
          aria-label="Sync progress"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct ?? undefined}
          data-indeterminate={pct === null ? "true" : "false"}
        >
          <span style={pct === null ? undefined : `width:${pct}%`} />
        </div>
      ) : null}
      {hasUpdate && updateDialogOpen.value ? <UpdateDialog /> : null}
    </header>
  );
}
