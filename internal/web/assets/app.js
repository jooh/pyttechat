(function () {
  const csrfToken = document.querySelector('meta[name="csrf-token"]').content;
  const messages = document.getElementById('messages');
  const form = document.getElementById('chat-form');
  const prompt = document.getElementById('prompt');
  const sendButton = document.getElementById('send-button');
  const stopButton = document.getElementById('stop-button');

  let currentTurn = null;
  let currentSource = null;

  function nearBottom() {
    return messages.scrollHeight - messages.scrollTop - messages.clientHeight < 96;
  }

  function scrollToBottom(force) {
    if (force || nearBottom()) {
      messages.scrollTop = messages.scrollHeight;
    }
  }

  function clearEmptyState() {
    const empty = messages.querySelector('.message-empty');
    if (empty) {
      empty.remove();
    }
  }

  function addMessage(role, text) {
    clearEmptyState();
    const article = document.createElement('article');
    article.className = `message message-${role}`;

    const label = document.createElement('div');
    label.className = 'message-label';
    label.textContent = role;

    const messageText = document.createElement('div');
    messageText.className = 'message-text';
    messageText.textContent = text || '';

    article.append(label, messageText);
    messages.append(article);
    scrollToBottom(true);
    return { article, text: messageText };
  }

  function ensureReasoning(article) {
    let details = article.querySelector('.reasoning');
    if (details) {
      return details.querySelector('.reasoning-content');
    }
    details = document.createElement('details');
    details.className = 'reasoning';

    const summary = document.createElement('summary');
    summary.textContent = 'Reasoning';

    const content = document.createElement('div');
    content.className = 'reasoning-content';

    details.append(summary, content);
    article.append(details);
    return content;
  }

  function setSubmitting(submitting) {
    sendButton.disabled = submitting;
    stopButton.disabled = !submitting || !currentTurn;
    prompt.disabled = submitting;
  }

  function closeSource() {
    if (currentSource) {
      currentSource.close();
      currentSource = null;
    }
  }

  function finishTurn() {
    closeSource();
    currentTurn = null;
    setSubmitting(false);
    prompt.disabled = false;
    prompt.focus();
  }

  async function submitPrompt(text) {
    const response = await fetch('/chat/turns', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        [csrfHeaderName()]: csrfToken,
      },
      body: JSON.stringify({ prompt: text }),
    });
    if (!response.ok) {
      throw new Error(await response.text());
    }
    return response.json();
  }

  function csrfHeaderName() {
    return 'X-CSRF-Token';
  }

  function subscribe(turn, assistant) {
    currentTurn = turn;
    currentSource = new EventSource(turn.stream_url);

    currentSource.addEventListener('text', function (event) {
      const data = JSON.parse(event.data);
      assistant.text.textContent += data.delta || '';
      scrollToBottom(false);
    });

    currentSource.addEventListener('reasoning', function (event) {
      const data = JSON.parse(event.data);
      ensureReasoning(assistant.article).textContent += data.delta || '';
      scrollToBottom(false);
    });

    currentSource.addEventListener('done', function () {
      finishTurn();
    });

    currentSource.addEventListener('aborted', function () {
      finishTurn();
    });

    currentSource.addEventListener('stream-error', function (event) {
      const data = JSON.parse(event.data);
      assistant.article.classList.add('message-error');
      assistant.text.textContent = data.message || 'The response stream failed.';
      finishTurn();
    });

    currentSource.onerror = function () {};
  }

  form.addEventListener('submit', async function (event) {
    event.preventDefault();

    const text = prompt.value.trim();
    if (!text || currentTurn) {
      return;
    }

    addMessage('user', text);
    const assistant = addMessage('assistant', '');
    prompt.value = '';
    setSubmitting(true);

    try {
      const turn = await submitPrompt(text);
      subscribe(turn, assistant);
      setSubmitting(true);
    } catch (error) {
      assistant.article.classList.add('message-error');
      assistant.text.textContent = 'The message could not be sent.';
      finishTurn();
    }
  });

  stopButton.addEventListener('click', async function () {
    if (!currentTurn) {
      return;
    }
    stopButton.disabled = true;
    try {
      await fetch(`/chat/turns/${encodeURIComponent(currentTurn.turn_id)}/abort`, {
        method: 'POST',
        headers: {
          [csrfHeaderName()]: csrfToken,
        },
      });
    } catch (error) {
      finishTurn();
    }
  });
})();
