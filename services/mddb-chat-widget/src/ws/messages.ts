import type { FeedbackMessage, JoinMessage } from './protocol';

/**
 * A `join`. With the host's identity token (#405) the server verifies who
 * the user is and resumes their stored conversation; without one the join is
 * anonymous, as before.
 */
export function joinMessage(name: string, scenario: string, identityToken?: string): JoinMessage {
  const msg: JoinMessage = { type: 'join', name, scenario };
  if (identityToken) msg.token = identityToken;
  return msg;
}

/** The longest feedback comment the server stores (#406). */
export const MAX_COMMENT_LENGTH = 2000;

/**
 * A rating of one answer. `messageId` is the `message_id` of the `done` frame
 * that ended the answer (#399), so the server can tie the rating to it; an
 * answer from before message ids existed has none and is rated without it.
 * `comment` (#406) is what the visitor wrote after a thumbs-down: trimmed,
 * cut at 2000 characters, and left out when empty.
 */
export function feedbackMessage(
  rating: 'up' | 'down',
  question: string,
  answer: string,
  messageId?: string,
  comment?: string,
): FeedbackMessage {
  const msg: FeedbackMessage = { type: 'feedback', rating, question, answer };
  if (messageId) msg.message_id = messageId;
  const text = Array.from((comment ?? '').trim()).slice(0, MAX_COMMENT_LENGTH).join('');
  if (text) msg.comment = text;
  return msg;
}

/** A message as the widget keeps it, as far as a rating needs it. */
interface Rated {
  role: string;
  content: string;
  messageId?: string;
}

/**
 * What a rating of the answer at `index` names: the answer, its id, and the
 * visitor's question — the closest user message before it.
 */
export function ratedTurn(
  messages: Rated[],
  index: number,
): { question: string; answer: string; messageId?: string } {
  let question = '';
  for (let i = Math.min(index, messages.length) - 1; i >= 0; i--) {
    if (messages[i].role === 'user') {
      question = messages[i].content;
      break;
    }
  }
  return { question, answer: messages[index]?.content || '', messageId: messages[index]?.messageId };
}
