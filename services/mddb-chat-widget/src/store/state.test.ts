// The widget's state: messages as they stream in, and the answer ids that
// feedback names (#399).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from './state.ts';

test('a new store is closed, disconnected and empty', () => {
  const s = new Store().getState();
  assert.equal(s.isOpen, false);
  assert.equal(s.isJoined, false);
  assert.deepEqual(s.messages, []);
});

test('updates and messages reach every subscriber until it unsubscribes', () => {
  const store = new Store();
  let seen = 0;
  const unsubscribe = store.subscribe(() => seen++);
  store.update({ isOpen: true });
  store.addMessage('user', 'hi');
  unsubscribe();
  store.update({ isOpen: false });

  assert.equal(seen, 2);
  assert.equal(store.getState().messages[0].content, 'hi');
});

test('chunks extend the answer being streamed, or start one', () => {
  const store = new Store();
  store.appendToLastAssistant('Hel');
  store.appendToLastAssistant('lo');
  store.addMessage('user', 'again');
  store.appendToLastAssistant('Second');

  assert.deepEqual(
    store.getState().messages.map((m) => [m.role, m.content]),
    [
      ['assistant', 'Hello'],
      ['user', 'again'],
      ['assistant', 'Second'],
    ],
  );
});

test("a finished answer is tagged with its done frame's message_id", () => {
  const store = new Store();
  store.appendToLastAssistant('answer');
  store.tagLastAssistant('ans-1');
  assert.equal(store.getState().messages[0].messageId, 'ans-1');
});

test('nothing is tagged without an id or without an answer to tag', () => {
  const store = new Store();
  store.tagLastAssistant('ans-1');
  assert.deepEqual(store.getState().messages, []);

  store.addMessage('user', 'q');
  store.tagLastAssistant('ans-1');
  assert.equal(store.getState().messages[0].messageId, undefined);

  store.appendToLastAssistant('a');
  store.tagLastAssistant(undefined);
  store.tagLastAssistant('');
  assert.equal(store.getState().messages[1].messageId, undefined);
});
