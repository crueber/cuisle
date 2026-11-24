// Shared by the whole-board rows (DoneList, BacklogList, BoardView lane rows): a click opens the ticket modal.
import { openTicketFromRow } from "../../state/ticketSteps";

export function openIssue(key: string): void {
  openTicketFromRow(key);
}
