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

/**
 * A rating of one answer. `messageId` is the `message_id` of the `done` frame
 * that ended the answer (#399), so the server can tie the rating to it; an
 * answer from before message ids existed has none and is rated without it.
 */
export function feedbackMessage(
  rating: 'up' | 'down',
  question: string,
  answer: string,
  messageId?: string,
): FeedbackMessage {
  const msg: FeedbackMessage = { type: 'feedback', rating, question, answer };
  if (messageId) msg.message_id = messageId;
  return msg;
}
