// Shared by the whole-board rows (DoneList, BacklogList, BoardView lane rows): open an issue in Jira.
import { api, toAppError } from "../../api";
import { showToast } from "../../state/ui";

export function openIssue(key: string): void {
  api()
    .app.openIssue(key)
    .catch((e: unknown) => {
      showToast({ message: toAppError(e).message, timeoutMs: 6000 });
    });
}
