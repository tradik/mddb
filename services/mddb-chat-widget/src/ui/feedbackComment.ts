// #406: the optional comment box shown under an answer after a thumbs-down.
//
// The rating has already been sent; the box only adds the visitor's words to
// it. Accessible: a visible label tied to the textarea, a 2000-character
// limit the browser enforces, Send and Skip buttons, and Escape to close.
// The caller moves the focus into it when it first opens.

/** The longest comment the server stores; mirrors MAX_COMMENT_LENGTH. */
const MAX_LENGTH = 2000;

export interface CommentBoxHandlers {
  /** A unique id for the textarea, so the label can name it. */
  id: string;
  /** What the visitor had typed before a re-render. */
  draft: string;
  onInput: (text: string) => void;
  onSend: (text: string) => void;
  onSkip: () => void;
}

/** The comment box: a form with a label, a textarea and two buttons. */
export function buildCommentBox(doc: Document, h: CommentBoxHandlers): HTMLFormElement {
  const form = doc.createElement('form');
  form.className = 'mddb-feedback-comment';

  const label = doc.createElement('label');
  label.setAttribute('for', h.id);
  label.textContent = 'What was wrong with this answer? (optional)';

  const text = doc.createElement('textarea');
  text.id = h.id;
  text.rows = 2;
  text.maxLength = MAX_LENGTH;
  text.value = h.draft;
  text.addEventListener('input', () => h.onInput(text.value));

  const send = doc.createElement('button');
  send.type = 'submit';
  send.textContent = 'Send';

  const skip = doc.createElement('button');
  skip.type = 'button';
  skip.textContent = 'Skip';
  skip.addEventListener('click', () => h.onSkip());

  form.addEventListener('submit', (e: Event) => {
    e.preventDefault();
    h.onSend(text.value);
  });
  form.addEventListener('keydown', (e: Event) => {
    if ((e as KeyboardEvent).key === 'Escape') h.onSkip();
  });

  const actions = doc.createElement('div');
  actions.className = 'mddb-feedback-comment-actions';
  actions.appendChild(send);
  actions.appendChild(skip);
  form.appendChild(label);
  form.appendChild(text);
  form.appendChild(actions);
  return form;
}
