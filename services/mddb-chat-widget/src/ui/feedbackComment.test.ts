// #406: the comment box after a thumbs-down, over a minimal fake DOM (the
// widget's tests run in Node without a browser).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildCommentBox } from './feedbackComment.ts';

type Listener = (e: unknown) => void;

class FakeElement {
  tag: string;
  children: FakeElement[] = [];
  attrs: Record<string, string> = {};
  listeners: Record<string, Listener> = {};
  [key: string]: unknown;
  constructor(tag: string) {
    this.tag = tag;
  }
  setAttribute(name: string, value: string): void {
    this.attrs[name] = value;
  }
  addEventListener(type: string, fn: Listener): void {
    this.listeners[type] = fn;
  }
  appendChild(child: FakeElement): void {
    this.children.push(child);
  }
}

const doc = { createElement: (tag: string) => new FakeElement(tag) } as unknown as Document;

function box(draft = '') {
  const calls: string[] = [];
  const form = buildCommentBox(doc, {
    id: 'c-1',
    draft,
    onInput: (t) => calls.push(`input:${t}`),
    onSend: (t) => calls.push(`send:${t}`),
    onSkip: () => calls.push('skip'),
  }) as unknown as FakeElement;
  const [label, text, actions] = form.children;
  const [send, skip] = actions.children;
  return { form, label, text, send, skip, calls };
}

test('the box is labelled, limited and keeps the draft', () => {
  const { form, label, text, send, skip } = box('half a thought');
  assert.equal(form.tag, 'form');
  assert.equal(form.className, 'mddb-feedback-comment');
  assert.equal(label.attrs.for, 'c-1');
  assert.match(String(label.textContent), /optional/);
  assert.equal(text.id, 'c-1');
  assert.equal(text.maxLength, 2000);
  assert.equal(text.value, 'half a thought');
  assert.equal(send.type, 'submit');
  assert.equal(send.textContent, 'Send');
  assert.equal(skip.type, 'button');
  assert.equal(skip.textContent, 'Skip');
});

test('typing, sending, skipping and Escape reach the handlers', () => {
  const { form, text, skip, calls } = box();
  text.value = 'wrong port';
  text.listeners.input({});
  let prevented = false;
  form.listeners.submit({ preventDefault: () => { prevented = true; } });
  skip.listeners.click({});
  form.listeners.keydown({ key: 'Enter' });
  form.listeners.keydown({ key: 'Escape' });
  assert.equal(prevented, true);
  assert.deepEqual(calls, ['input:wrong port', 'send:wrong port', 'skip', 'skip']);
});
