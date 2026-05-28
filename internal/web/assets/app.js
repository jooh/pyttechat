(function () {
  const csrfToken = document.querySelector('meta[name="csrf-token"]').content;
  const messages = document.getElementById('messages');
  const messagesEnd = document.getElementById('messages-end');
  const scrollButton = document.getElementById('scroll-bottom');
  const form = document.getElementById('chat-form');
  const prompt = document.getElementById('prompt');
  const sendButton = document.getElementById('send-button');
  const stopButton = document.getElementById('stop-button');
  const composerStatus = document.getElementById('composer-status');

  const copyIcon = '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M8 7h10v13H8z"></path><path d="M6 17H4V3h12v2"></path></svg>';

  let currentTurn = null;
  let currentUser = null;
  let currentAssistant = null;
  let currentSource = null;
  let streamErrorTimer = null;
  let abortRequested = false;
  let creatingTurn = false;
  let statusIDCounter = 0;

  function csrfHeaderName() {
    return 'X-CSRF-Token';
  }

  function setStatus(text) {
    if (composerStatus) {
      composerStatus.textContent = text;
    }
  }

  function isNearBottom() {
    return messages.scrollHeight - messages.scrollTop - messages.clientHeight < 96;
  }

  function updateScrollButton() {
    if (scrollButton) {
      scrollButton.hidden = isNearBottom();
    }
  }

  function scrollToBottom(force, wasNearBottom) {
    if (force || wasNearBottom) {
      messages.scrollTop = messages.scrollHeight;
    }
    updateScrollButton();
  }

  function insertMessage(article) {
    messages.insertBefore(article, messagesEnd || null);
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
    text.className = 'message-text message-plain';
    text.textContent = 'Start a conversation.';

    article.append(text);
    insertMessage(article);
    updateScrollButton();
  }

  function removeMessage(message) {
    if (message && message.article && message.article.parentNode === messages) {
      message.article.remove();
      ensureEmptyState();
      updateScrollButton();
    }
  }

  function discardTurn(user, assistant) {
    removeMessage(assistant);
    removeMessage(user);
  }

  function createMessageActions() {
    const actions = document.createElement('div');
    actions.className = 'message-actions';
    actions.setAttribute('aria-label', 'Message actions');

    const copy = document.createElement('button');
    copy.className = 'message-action';
    copy.type = 'button';
    copy.dataset.copyMessage = '';
    copy.setAttribute('aria-label', 'Copy message');
    copy.innerHTML = `${copyIcon}<span class="sr-only">Copy message</span>`;

    actions.append(copy);
    return actions;
  }

  function nextStatusContentID() {
    statusIDCounter += 1;
    return `message-status-content-${statusIDCounter}`;
  }

  function createThinkingStatus() {
    const status = document.createElement('div');
    status.className = 'message-status';
    status.dataset.statusKind = 'thinking';
    status.dataset.statusState = 'active';

    const toggle = document.createElement('button');
    toggle.className = 'message-status-toggle status-sweep';
    toggle.type = 'button';
    toggle.dataset.statusToggle = '';
    toggle.textContent = 'thinking...';
    toggle.setAttribute('aria-expanded', 'false');

    const content = document.createElement('div');
    content.className = 'message-status-content';
    content.id = nextStatusContentID();
    content.hidden = true;

    toggle.setAttribute('aria-controls', content.id);
    status.append(toggle, content);
    return status;
  }

  function ensureThinkingStatus(article) {
    let status = article.querySelector('.message-status[data-status-kind="thinking"]');
    if (!status) {
      status = createThinkingStatus();
      const body = article.querySelector('.message-text');
      article.insertBefore(status, body);
    }
    return status.querySelector('.message-status-content');
  }

  function completeThinkingStatus(article) {
    const status = article.querySelector('.message-status[data-status-kind="thinking"]');
    if (!status) {
      return;
    }
    const content = status.querySelector('.message-status-content');
    if (!content || !content.textContent) {
      status.remove();
      return;
    }
    const toggle = status.querySelector('.message-status-toggle');
    status.dataset.statusState = 'complete';
    if (toggle) {
      toggle.classList.remove('status-sweep');
      toggle.textContent = 'thinking';
      toggle.setAttribute('aria-expanded', 'false');
    }
    content.hidden = true;
  }

  function toggleStatusContent(toggle) {
    const contentID = toggle.getAttribute('aria-controls');
    const content = contentID
      ? document.getElementById(contentID)
      : toggle.closest('.message-status')?.querySelector('.message-status-content');
    if (!content) {
      return;
    }
    const expanded = toggle.getAttribute('aria-expanded') === 'true';
    toggle.setAttribute('aria-expanded', String(!expanded));
    content.hidden = expanded;
  }

  function addMessage(role, text, options) {
    clearEmptyState();
    const article = document.createElement('article');
    article.className = `message message-${role}`;
    if (options && options.streaming) {
      article.classList.add('message-streaming');
    }

    const messageText = document.createElement('div');
    messageText.className = role === 'assistant' ? 'message-text markdown-body' : 'message-text message-plain';
    messageText.textContent = text || '';

    if (role === 'assistant' && options && options.streaming) {
      article.append(createThinkingStatus());
    }
    article.append(messageText, createMessageActions());
    insertMessage(article);
    scrollToBottom(true, true);
    return { article, text: messageText };
  }

  function assignMessageIDs(user, assistant, turn) {
    if (turn.user_message_id) {
      user.article.id = `message-${turn.user_message_id}`;
      user.article.dataset.messageId = turn.user_message_id;
    }
    if (turn.assistant_message_id) {
      assistant.article.id = `message-${turn.assistant_message_id}`;
      assistant.article.dataset.messageId = turn.assistant_message_id;
      assistant.text.id = `message-body-${turn.assistant_message_id}`;
    }
  }

  function assistantHasContent(assistant) {
    if (!assistant) {
      return false;
    }
    if (assistant.text.textContent || assistant.text.innerHTML) {
      return true;
    }
    const status = assistant.article.querySelector('.message-status-content');
    return Boolean(status && status.textContent);
  }

  function setCompletedAt(assistant, completedAt) {
    if (!completedAt) {
      return;
    }
    let timestamp = assistant.article.querySelector('.message-completed-at');
    if (!timestamp) {
      timestamp = document.createElement('time');
      timestamp.className = 'message-completed-at';
      const actions = assistant.article.querySelector('.message-actions');
      assistant.article.insertBefore(timestamp, actions || null);
    }
    const date = new Date(completedAt);
    timestamp.dateTime = completedAt;
    timestamp.textContent = Number.isNaN(date.getTime()) ? completedAt : `Completed ${date.toLocaleString()}`;
  }

  function updateComposerState() {
    const submitting = Boolean(currentTurn) || creatingTurn;
    sendButton.disabled = submitting || prompt.value.trim() === '';
    stopButton.disabled = !currentTurn || abortRequested;
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

  function finishTurn(status) {
    clearStreamErrorTimer();
    closeSource();
    currentTurn = null;
    currentUser = null;
    currentAssistant = null;
    abortRequested = false;
    creatingTurn = false;
    updateComposerState();
    setStatus(status || 'Ready');
  }

  function markTurnError(assistant, message) {
    assistant.article.classList.remove('message-streaming');
    assistant.article.classList.add('message-error');
    const status = assistant.article.querySelector('.message-status');
    if (status) {
      status.remove();
    }
    const error = document.createElement('p');
    error.className = 'message-error-detail';
    error.textContent = message;
    assistant.text.replaceChildren(error);
  }

  function languageFromCode(code) {
    if (!code) {
      return 'code';
    }
    for (const className of code.classList) {
      if (className.indexOf('language-') === 0) {
        return className.replace('language-', '') || 'code';
      }
    }
    return code.getAttribute('data-lang') || 'code';
  }

  function enhanceCodeBlocks(root) {
    root.querySelectorAll('pre').forEach(function (pre) {
      if (pre.closest('.code-block')) {
        return;
      }
      const code = pre.querySelector('code');
      const wrapper = document.createElement('div');
      wrapper.className = 'code-block';

      const header = document.createElement('div');
      header.className = 'code-block-header';

      const language = document.createElement('span');
      language.className = 'code-block-language';
      language.textContent = languageFromCode(code);

      const copy = document.createElement('button');
      copy.className = 'copy-code';
      copy.type = 'button';
      copy.dataset.copyCode = '';
      copy.textContent = 'Copy';
      copy.setAttribute('aria-label', 'Copy code');

      header.append(language, copy);
      pre.parentNode.insertBefore(wrapper, pre);
      wrapper.append(header, pre);
    });
  }

  function enhanceMessage(article) {
    const body = article.querySelector('.markdown-body');
    if (body) {
      enhanceCodeBlocks(body);
    }
  }

  function enhanceAllMessages() {
    messages.querySelectorAll('.message').forEach(enhanceMessage);
  }

  async function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return;
    }

    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.top = '-1000px';
    document.body.append(textarea);
    textarea.select();
    document.execCommand('copy');
    textarea.remove();
  }

  function messageCopyText(body) {
    const clone = body.cloneNode(true);
    clone.querySelectorAll('.code-block-header').forEach(function (header) {
      header.remove();
    });
    return clone.innerText || clone.textContent || '';
  }

  function setCopyFeedback(button, copiedText) {
    const originalLabel = button.dataset.originalLabel || button.getAttribute('aria-label') || '';
    button.dataset.originalLabel = originalLabel;
    button.dataset.copyState = 'copied';
    button.setAttribute('aria-label', copiedText);
    if (button.classList.contains('copy-code')) {
      button.textContent = 'Copied';
    }

    if (button.copyTimer) {
      clearTimeout(button.copyTimer);
    }
    button.copyTimer = setTimeout(function () {
      delete button.dataset.copyState;
      button.setAttribute('aria-label', originalLabel);
      if (button.classList.contains('copy-code')) {
        button.textContent = 'Copy';
      }
    }, 1400);
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
    updateComposerState();
    setStatus('Stopping response');
    try {
      await abortTurn(turn);
    } catch (error) {
      if (currentTurn === turn) {
        abortRequested = false;
        updateComposerState();
        setStatus('Generating response');
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
        finishTurn('Stream disconnected');
      }
      return;
    }
    if (currentTurn === turn) {
      discardTurn(user, assistant);
      finishTurn('Stream disconnected');
    }
  }

  function subscribe(turn, user, assistant) {
    currentTurn = turn;
    currentUser = user;
    currentAssistant = assistant;
    abortRequested = false;
    currentSource = new EventSource(turn.stream_url);
    setStatus('Connecting');

    currentSource.onopen = function () {
      clearStreamErrorTimer();
      setStatus('Generating response');
    };

    currentSource.addEventListener('preview', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      const wasNearBottom = isNearBottom();
      const data = JSON.parse(event.data);
      if (data.assistant_message_id && !assistant.article.dataset.messageId) {
        assistant.article.id = `message-${data.assistant_message_id}`;
        assistant.article.dataset.messageId = data.assistant_message_id;
        assistant.text.id = `message-body-${data.assistant_message_id}`;
      }
      assistant.text.innerHTML = data.html || '';
      enhanceMessage(assistant.article);
      scrollToBottom(false, wasNearBottom);
    });

    currentSource.addEventListener('reasoning', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      const wasNearBottom = isNearBottom();
      const data = JSON.parse(event.data);
      ensureThinkingStatus(assistant.article).textContent += data.delta || '';
      scrollToBottom(false, wasNearBottom);
    });

    currentSource.addEventListener('done', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      const wasNearBottom = isNearBottom();
      const data = JSON.parse(event.data);
      if (typeof data.html === 'string') {
        assistant.text.innerHTML = data.html;
        enhanceMessage(assistant.article);
      }
      setCompletedAt(assistant, data.completed_at);
      completeThinkingStatus(assistant.article);
      assistant.article.classList.remove('message-streaming');
      assistant.article.classList.add('message-complete');
      if (!assistantHasContent(assistant)) {
        removeMessage(assistant);
      }
      scrollToBottom(false, wasNearBottom);
      finishTurn('Response complete');
    });

    currentSource.addEventListener('aborted', function () {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      discardTurn(user, assistant);
      finishTurn('Response stopped');
    });

    currentSource.addEventListener('stream-error', function (event) {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      let message = 'The response stream failed.';
      try {
        const data = JSON.parse(event.data);
        message = data.message || message;
      } catch (error) {
        message = 'The response stream failed.';
      }
      markTurnError(assistant, message);
      finishTurn('Stream failed');
    });

    currentSource.onerror = function () {
      if (streamErrorTimer || !currentTurn || abortRequested) {
        return;
      }
      setStatus('Reconnecting');
      streamErrorTimer = setTimeout(function () {
        streamErrorTimer = null;
        abortDisconnectedTurn(turn, user, assistant);
      }, 10000);
    };
  }

  function syncPromptHeight() {
    prompt.style.height = 'auto';
    const maxHeight = parseFloat(window.getComputedStyle(prompt).maxHeight);
    const nextHeight = Number.isFinite(maxHeight) ? Math.min(prompt.scrollHeight, maxHeight) : prompt.scrollHeight;
    prompt.style.height = `${nextHeight}px`;
    prompt.style.overflowY = Number.isFinite(maxHeight) && prompt.scrollHeight > maxHeight ? 'auto' : 'hidden';
  }

  form.addEventListener('submit', async function (event) {
    event.preventDefault();

    const text = prompt.value.trim();
    if (!text || currentTurn) {
      return;
    }

    const user = addMessage('user', text);
    const assistant = addMessage('assistant', '', { streaming: true });
    currentUser = user;
    currentAssistant = assistant;
    prompt.value = '';
    syncPromptHeight();
    creatingTurn = true;
    updateComposerState();
    setStatus('Starting response');

    try {
      const turn = await submitPrompt(text);
      assignMessageIDs(user, assistant, turn);
      creatingTurn = false;
      subscribe(turn, user, assistant);
      updateComposerState();
    } catch (error) {
      creatingTurn = false;
      discardTurn(user, assistant);
      finishTurn('Message not sent');
    }
  });

  prompt.addEventListener('keydown', function (event) {
    if (event.key !== 'Enter' || event.shiftKey || event.ctrlKey || event.metaKey || event.altKey || event.isComposing || event.keyCode === 229) {
      return;
    }
    event.preventDefault();
    if (!currentTurn) {
      form.requestSubmit();
    }
  });

  prompt.addEventListener('input', function () {
    syncPromptHeight();
    updateComposerState();
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
        finishTurn('Response stopped');
      }
    } catch (error) {
      if (currentTurn === turn && currentAssistant) {
        markTurnError(currentAssistant, 'The turn could not be stopped.');
        finishTurn('Stop failed');
      }
    }
  });

  messages.addEventListener('scroll', updateScrollButton, { passive: true });

  if (scrollButton) {
    scrollButton.addEventListener('click', function () {
      scrollToBottom(true, true);
    });
  }

  document.addEventListener('click', async function (event) {
    if (!(event.target instanceof Element)) {
      return;
    }

    const copyMessage = event.target.closest('[data-copy-message]');
    if (copyMessage) {
      const article = copyMessage.closest('.message');
      const body = article && article.querySelector('.message-text');
      const text = body ? messageCopyText(body) : '';
      if (text) {
        try {
          await copyText(text);
          setCopyFeedback(copyMessage, 'Copied message');
        } catch (error) {
          setStatus('Copy failed');
        }
      }
      return;
    }

    const statusToggle = event.target.closest('[data-status-toggle]');
    if (statusToggle) {
      toggleStatusContent(statusToggle);
      return;
    }

    const copyCode = event.target.closest('[data-copy-code]');
    if (copyCode) {
      const block = copyCode.closest('.code-block');
      const code = block && block.querySelector('code');
      const text = code ? code.textContent || '' : '';
      if (text) {
        try {
          await copyText(text);
          setCopyFeedback(copyCode, 'Copied code');
        } catch (error) {
          setStatus('Copy failed');
        }
      }
    }
  });

  enhanceAllMessages();
  syncPromptHeight();
  updateComposerState();
  updateScrollButton();
})();
