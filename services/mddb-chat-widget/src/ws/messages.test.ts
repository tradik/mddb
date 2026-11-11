// #405 and #399: what the widget sends when it joins and when it rates.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { feedbackMessage, joinMessage, MAX_COMMENT_LENGTH, ratedTurn } from './messages.ts';

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

test('a comment is sent trimmed and capped, and an empty one not at all (#406)', () => {
  assert.equal(feedbackMessage('down', 'q', 'a', 'ans-1', '  Wrong port.  ').comment, 'Wrong port.');
  assert.equal('comment' in feedbackMessage('down', 'q', 'a', 'ans-1', '   '), false);
  assert.equal('comment' in feedbackMessage('down', 'q', 'a', 'ans-1'), false);
  const long = 'ż'.repeat(MAX_COMMENT_LENGTH + 5);
  assert.equal(Array.from(feedbackMessage('down', 'q', 'a', undefined, long).comment ?? '').length, MAX_COMMENT_LENGTH);
});

test('a rating names the answer, its id and the question before it', () => {
  const messages = [
    { role: 'user', content: 'first' },
    { role: 'assistant', content: 'one' },
    { role: 'user', content: 'second' },
    { role: 'assistant', content: 'two', messageId: 'ans-2' },
  ];
  assert.deepEqual(ratedTurn(messages, 3), { question: 'second', answer: 'two', messageId: 'ans-2' });
  assert.deepEqual(ratedTurn([{ role: 'assistant', content: 'hi' }], 0), { question: '', answer: 'hi', messageId: undefined });
  assert.deepEqual(ratedTurn([], 4), { question: '', answer: '', messageId: undefined });
});
