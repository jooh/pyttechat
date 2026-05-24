(function () {
  const csrfToken = document.querySelector('meta[name="csrf-token"]').content;
  const messages = document.getElementById('messages');
  const form = document.getElementById('chat-form');
  const prompt = document.getElementById('prompt');
  const sendButton = document.getElementById('send-button');
  const stopButton = document.getElementById('stop-button');

  let currentTurn = null;
  let currentUser = null;
  let currentAssistant = null;
  let currentSource = null;
  let streamErrorTimer = null;
  let abortRequested = false;

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

  function ensureEmptyState() {
    if (messages.querySelector('.message')) {
      return;
    }
    const article = document.createElement('article');
    article.className = 'message message-empty';

    const text = document.createElement('div');
    text.className = 'message-text';
    text.textContent = 'Start a conversation.';

    article.append(text);
    messages.append(article);
  }

  function removeMessage(message) {
    if (message && message.article && message.article.parentNode === messages) {
      message.article.remove();
      ensureEmptyState();
    }
  }

  function discardTurn(user, assistant) {
    removeMessage(assistant);
    removeMessage(user);
  }

  function assistantHasContent(assistant) {
    if (!assistant) {
      return false;
    }
    if (assistant.text.textContent) {
      return true;
    }
    const reasoning = assistant.article.querySelector('.reasoning-content');
    return Boolean(reasoning && reasoning.textContent);
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
    stopButton.disabled = !submitting || !currentTurn || abortRequested;
    prompt.disabled = submitting;
  }

  function closeSource() {
    if (currentSource) {
      currentSource.close();
      currentSource = null;
    }
  }

  function clearStreamErrorTimer() {
    if (streamErrorTimer) {
      clearTimeout(streamErrorTimer);
      streamErrorTimer = null;
    }
  }

  function finishTurn() {
    clearStreamErrorTimer();
    closeSource();
    currentTurn = null;
    currentUser = null;
    currentAssistant = null;
    abortRequested = false;
    setSubmitting(false);
    prompt.disabled = false;
    prompt.focus();
  }

  function markTurnError(assistant, message) {
    assistant.article.classList.add('message-error');
    if (!assistant.text.textContent) {
      assistant.text.textContent = message;
    }
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

  async function abortTurn(turn) {
    const response = await fetch(`/chat/turns/${encodeURIComponent(turn.turn_id)}/abort`, {
      method: 'POST',
      headers: {
        [csrfHeaderName()]: csrfToken,
      },
    });
    if (!response.ok) {
      throw new Error((await response.text()) || 'The turn could not be stopped.');
    }
  }

  async function requestAbort(turn) {
    abortRequested = true;
    setSubmitting(true);
    try {
      await abortTurn(turn);
    } catch (error) {
      if (currentTurn === turn) {
        abortRequested = false;
        setSubmitting(true);
      }
      throw error;
    }
  }

  async function abortDisconnectedTurn(turn, user, assistant) {
    if (currentTurn !== turn) {
      return;
    }
    try {
      await requestAbort(turn);
    } catch (error) {
      if (currentTurn === turn) {
        markTurnError(assistant, 'The response stream disconnected, and the turn could not be stopped.');
      }
      return;
    }
    if (currentTurn === turn) {
      discardTurn(user, assistant);
      finishTurn();
    }
  }

  function subscribe(turn, user, assistant) {
    currentTurn = turn;
    currentUser = user;
    currentAssistant = assistant;
    abortRequested = false;
    currentSource = new EventSource(turn.stream_url);

    currentSource.onopen = function () {
      clearStreamErrorTimer();
    };

    currentSource.addEventListener('text', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      const data = JSON.parse(event.data);
      assistant.text.textContent += data.delta || '';
      scrollToBottom(false);
    });

    currentSource.addEventListener('reasoning', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      const data = JSON.parse(event.data);
      ensureReasoning(assistant.article).textContent += data.delta || '';
      scrollToBottom(false);
    });

    currentSource.addEventListener('done', function () {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      assistant.article.classList.add('message-complete');
      if (!assistantHasContent(assistant)) {
        removeMessage(assistant);
      }
      finishTurn();
    });

    currentSource.addEventListener('aborted', function () {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      discardTurn(user, assistant);
      finishTurn();
    });

    currentSource.addEventListener('stream-error', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      discardTurn(user, assistant);
      finishTurn();
    });

    currentSource.onerror = function () {
      if (streamErrorTimer || !currentTurn || abortRequested) {
        return;
      }
      streamErrorTimer = setTimeout(function () {
        streamErrorTimer = null;
        abortDisconnectedTurn(turn, user, assistant);
      }, 10000);
    };
  }

  form.addEventListener('submit', async function (event) {
    event.preventDefault();

    const text = prompt.value.trim();
    if (!text || currentTurn) {
      return;
    }

    const user = addMessage('user', text);
    const assistant = addMessage('assistant', '');
    currentUser = user;
    currentAssistant = assistant;
    prompt.value = '';
    setSubmitting(true);

    try {
      const turn = await submitPrompt(text);
      subscribe(turn, user, assistant);
      setSubmitting(true);
    } catch (error) {
      discardTurn(user, assistant);
      finishTurn();
    }
  });

  stopButton.addEventListener('click', async function () {
    if (!currentTurn) {
      return;
    }
    const turn = currentTurn;
    try {
      await requestAbort(turn);
      if (currentTurn === turn) {
        discardTurn(currentUser, currentAssistant);
        finishTurn();
      }
    } catch (error) {
      if (currentTurn === turn && currentAssistant) {
        markTurnError(currentAssistant, 'The turn could not be stopped.');
      }
    }
  });
})();
