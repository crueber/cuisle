// The menu form of the standup tabs (ux-spec §4 "Menu"): one menu-variant Combobox over every standup, with the
// hint "1–N to switch" beside it.
import type { StandupTab } from "../../api/types";
import { Combobox } from "../../components/Combobox";
import { Icon } from "../../components/Icon";
import type { StandupSwitcherProps } from "./props";

export function StandupSwitcher(props: StandupSwitcherProps) {
  const { tabs, activeId, onSelect } = props;
  const index = tabs.findIndex((t) => t.id === activeId);
  const active: StandupTab | null = index >= 0 ? (tabs[index] ?? null) : null;
  const total = tabs.length;

  const menuLabel = active ? (
    <>
      <span class="sf-tab__num">{index + 1}</span>{" "}
      <span class="sf-switch__name">{active.name}</span>{" "}
      <span class="sf-switch__pos">
        {index + 1}/{total}
      </span>{" "}
      <Icon name="chevron-down" size={16} />
    </>
  ) : (
    <>
      <span class="sf-switch__name">Standups</span>
      <Icon name="chevron-down" size={16} />
    </>
  );

  return (
    <div class="sf-switch">
      <Combobox<StandupTab>
        id="standup-switcher"
        label="Switch standup"
        variant="menu"
        value={active}
        options={tabs}
        getKey={(t) => String(t.id)}
        getLabel={(t) => t.name}
        onSelect={(t) => onSelect(t.id)}
        menuLabel={menuLabel}
      />
      <span class="sf-switch__hint">{`1–${total} to switch`}</span>
    </div>
  );
}
