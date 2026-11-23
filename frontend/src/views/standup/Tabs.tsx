// Header standup tabs: full, then key-only, then the switcher menu, by measured fit (ux-spec §4). Only configured
// standups reach here (Header filters drafts). Pure choice logic lives in layout/tabs.ts.
import type { StandupTab } from "../../api/types";
import { Combobox } from "../../components/Combobox";
import { Icon } from "../../components/Icon";
import { tabMode, visibleTabs } from "../../layout/tabs";
import { StandupSwitcher } from "./StandupSwitcher";
import type { TabsProps } from "./props";
import { MORE_WIDTH, tabMeasure } from "./tabWidths";

const MAX_TABS = 7;

export function Tabs(props: TabsProps) {
  const { tabs, activeId, availW, onSelect, onAdd } = props;
  const activeIndex = Math.max(0, tabs.findIndex((t) => t.id === activeId));
  const vis = visibleTabs(tabs.length, activeIndex, MAX_TABS);
  const shown = vis.indices.map((i) => ({ tab: tabs[i]!, index: i }));
  const hidden = tabs.filter((_, i) => !vis.indices.includes(i));
  const mode = tabMode(
    shown.map((s) => tabMeasure(s.tab)),
    availW,
    vis.more > 0 ? MORE_WIDTH : 0,
  );

  return (
    <nav class="sf-tabs" aria-label="Standups" data-mode={mode}>
      {mode === "menu" ? (
        <StandupSwitcher tabs={tabs} activeId={activeId} onSelect={onSelect} />
      ) : (
        shown.map(({ tab, index }) => {
          const active = tab.id === activeId;
          const keyOnly = mode === "key";
          return (
            <button
              key={tab.id}
              type="button"
              class="sf-tab"
              aria-pressed={active ? "true" : "false"}
              aria-label={keyOnly ? `${index + 1} ${tab.spaceKey} — ${tab.name}` : undefined}
              title={keyOnly ? tab.name : undefined}
              onClick={() => onSelect(tab.id)}
            >
              <span class="sf-tab__num">{index + 1}</span>{" "}
              {keyOnly ? null : (
                <>
                  <span class="sf-tab__name">{tab.name}</span>{" "}
                </>
              )}
              <span class="sf-tab__key">{tab.spaceKey}</span>
            </button>
          );
        })
      )}
      {mode !== "menu" && vis.more > 0 ? (
        <Combobox<StandupTab>
          id="standup-more"
          label="More standups"
          variant="menu"
          value={null}
          options={hidden}
          getKey={(t) => String(t.id)}
          getLabel={(t) => t.name}
          onSelect={(t) => onSelect(t.id)}
          menuLabel={`+${vis.more} more`}
        />
      ) : null}
      <button type="button" class="sf-tabs__add" aria-label="Add a standup" onClick={() => onAdd()}>
        <Icon name="plus" size={18} strokeWidth={2.6} />
      </button>
    </nav>
  );
}
