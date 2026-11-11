// #405 and #399: what the widget sends when it joins and when it rates.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { feedbackMessage, joinMessage } from './messages.ts';

test('an anonymous join carries no token', () => {
  assert.deepEqual(joinMessage('Ada', 'support'), {
    type: 'join',
    name: 'Ada',
    scenario: 'support',
  });
  assert.equal('token' in joinMessage('Ada', 'support', ''), false);
});

test("a signed join carries the host's identity token", () => {
  assert.deepEqual(joinMessage('Ada', 'support', 'v1.a.b'), {
    type: 'join',
    name: 'Ada',
    scenario: 'support',
    token: 'v1.a.b',
  });
});

test('feedback names the answer it rates when the done frame named it', () => {
  assert.deepEqual(feedbackMessage('up', 'q', 'a', 'ans-1'), {
    type: 'feedback',
    rating: 'up',
    question: 'q',
    answer: 'a',
    message_id: 'ans-1',
  });
});

test('feedback on an answer without an id is sent without one', () => {
  const msg = feedbackMessage('down', 'q', 'a');
  assert.equal(msg.rating, 'down');
  assert.equal('message_id' in msg, false);
});
