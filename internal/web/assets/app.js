(function () {
  const csrfToken = document.querySelector('meta[name="csrf-token"]').content;
  const messages = document.getElementById('messages');
  const messagesEnd = document.getElementById('messages-end');
  const scrollButton = document.getElementById('scroll-bottom');
  const composerDock = document.getElementById('composer-dock');
  const form = document.getElementById('chat-form');
  const prompt = document.getElementById('prompt');
  const actionButton = document.getElementById('composer-action');
  const actionIcons = actionButton ? actionButton.querySelectorAll('[data-action-icon]') : [];
  const undoButton = document.getElementById('undo-button');
  const redoButton = document.getElementById('redo-button');
  const ffwdButton = document.getElementById('ffwd-button');
  const composerStatus = document.getElementById('composer-status');
  const composerEndTarget = document.getElementById('composer-end-target');
  const dirtyDialog = document.getElementById('dirty-dialog');
  const themeToggle = document.querySelector('[data-theme-toggle]');
  const themeIcons = themeToggle ? themeToggle.querySelectorAll('[data-theme-icon]') : [];

  const themeStorageKey = 'pyttechat.theme';
  const copyIcon = '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M8 7h10v13H8z"></path><path d="M6 17H4V3h12v2"></path></svg>';
  const nearBottomThreshold = 32;
  const systemThemeQuery = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

  let currentTurn = null;
  let currentUser = null;
  let currentAssistant = null;
  let currentSource = null;
  let streamErrorTimer = null;
  let abortRequested = false;
  let creatingTurn = false;
  let statusIDCounter = 0;
  let nextMessageIndex = initialNextMessageIndex();
  let currentEditIndex = null;
  let originalPromptValue = '';
  let dockPromptValue = '';
  let pendingNavigation = null;
  let mermaidInitialized = false;
  let mermaidCurrentTheme = '';
  let mermaidIDCounter = 0;
  let themeOverride = '';

  function csrfHeaderName() {
    return 'X-CSRF-Token';
  }

  function isTheme(value) {
    return value === 'light' || value === 'dark';
  }

  function storedTheme() {
    if (themeOverride) {
      return themeOverride;
    }
    try {
      const theme = window.localStorage.getItem(themeStorageKey);
      return isTheme(theme) ? theme : '';
    } catch (error) {
      return '';
    }
  }

  function systemTheme() {
    return systemThemeQuery && systemThemeQuery.matches ? 'dark' : 'light';
  }

  function currentTheme() {
    return storedTheme() || systemTheme();
  }

  function applyThemePreference() {
    const theme = storedTheme();
    if (theme) {
      document.documentElement.dataset.theme = theme;
    } else {
      delete document.documentElement.dataset.theme;
    }
    updateThemeToggle();
  }

  function updateThemeToggle() {
    if (!themeToggle) {
      return;
    }
    const theme = currentTheme();
    const nextTheme = theme === 'dark' ? 'light' : 'dark';
    themeToggle.dataset.themeCurrent = theme;
    themeToggle.setAttribute('aria-label', `Current theme: ${theme}. Switch to ${nextTheme} mode`);
    themeToggle.title = `Switch to ${nextTheme} mode`;
    themeIcons.forEach(function (icon) {
      icon.hidden = icon.dataset.themeIcon !== theme;
    });
  }

  function lockTheme(theme) {
    if (!isTheme(theme)) {
      return;
    }
    themeOverride = theme;
    try {
      window.localStorage.setItem(themeStorageKey, theme);
    } catch (error) {
      // Apply the choice for this page even if browser storage is unavailable.
    }
    document.documentElement.dataset.theme = theme;
    updateThemeToggle();
    refreshMermaidThemes();
  }

  function toggleTheme() {
    lockTheme(currentTheme() === 'dark' ? 'light' : 'dark');
  }

  function setStatus(text) {
    if (composerStatus) {
      composerStatus.textContent = text || '';
    }
  }

  function messageIndex(article) {
    const value = Number.parseInt(article?.dataset.messageIndex || '', 10);
    return Number.isFinite(value) ? value : -1;
  }

  function initialNextMessageIndex() {
    let maxIndex = -1;
    messages.querySelectorAll('.message[data-message-index]').forEach(function (article) {
      maxIndex = Math.max(maxIndex, messageIndex(article));
    });
    const serverIndex = Number.parseInt(messages.dataset.nextMessageIndex || '', 10);
    if (Number.isFinite(serverIndex) && serverIndex >= 0) {
      return Math.max(serverIndex, maxIndex + 1);
    }
    return maxIndex + 1;
  }

  function transcriptPosition() {
    return currentEditIndex === null ? nextMessageIndex : currentEditIndex;
  }

  function userMessages() {
    return Array.from(messages.querySelectorAll('.message-user[data-editable-prompt="true"][data-message-index]'))
      .sort(function (left, right) {
        return messageIndex(left) - messageIndex(right);
      });
  }

  function userMessageBefore(index) {
    let target = null;
    userMessages().forEach(function (article) {
      if (messageIndex(article) < index) {
        target = article;
      }
    });
    return target;
  }

  function userMessageAfter(index) {
    return userMessages().find(function (article) {
      return messageIndex(article) > index;
    }) || null;
  }

  function promptTextForArticle(article) {
    const body = article?.querySelector('.message-text');
    return body ? body.textContent || '' : '';
  }

  function updatePromptHistoryState() {
    const activeIndex = currentEditIndex;
    const data = composerDock.dataset;
    if (activeIndex === null) {
      data.endActive = 'true';
      delete data.composerDetached;
    } else {
      delete data.endActive;
      data.composerDetached = 'true';
    }
    if (composerEndTarget) {
      composerEndTarget.hidden = activeIndex === null;
    }
    messages.querySelectorAll('.message[data-message-index]').forEach(function (article) {
      const index = messageIndex(article);
      const data = article.dataset;
      if (activeIndex !== null && index === activeIndex && article.matches('.message-user[data-editable-prompt="true"]')) {
        data.activePrompt = 'true';
      } else {
        delete data.activePrompt;
      }

      if (activeIndex !== null && index > activeIndex) {
        data.afterActivePrompt = 'true';
      } else {
        delete data.afterActivePrompt;
      }
    });
  }

  function hasDirtyPrompt() {
    return prompt.value !== originalPromptValue;
  }

  function isNearBottom() {
    return messages.scrollHeight - messages.scrollTop - messages.clientHeight < nearBottomThreshold;
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
      updatePromptHistoryState();
      updateScrollButton();
    }
  }

  function discardTurn(user, assistant) {
    removeMessage(assistant);
    removeMessage(user);
  }

  function articleForEditIndex(index) {
    return messages.querySelector(`.message-user[data-editable-prompt="true"][data-message-index="${index}"]`);
  }

  function editSlotFor(article) {
    let slot = article.querySelector(':scope > .message-edit-slot');
    if (!slot) {
      slot = document.createElement('div');
      slot.className = 'message-edit-slot';
      const actions = article.querySelector(':scope > .message-actions');
      article.insertBefore(slot, actions || null);
    }
    return slot;
  }

  function clearEditingArticle() {
    const editing = messages.querySelector('.message-user[data-editing="true"]');
    if (editing) {
      delete editing.dataset.editing;
    }
  }

  function animateComposerFrom(firstRect) {
    if (!firstRect || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      return;
    }
    const box = form.querySelector('.composer-box');
    if (!box) {
      return;
    }
    const lastRect = form.getBoundingClientRect();
    const dx = firstRect.left - lastRect.left;
    const dy = firstRect.top - lastRect.top;
    if (Math.abs(dx) < 1 && Math.abs(dy) < 1) {
      return;
    }
    box.style.transition = 'none';
    box.style.transform = `translate(${dx}px, ${dy}px)`;
    window.requestAnimationFrame(function () {
      box.style.transition = 'transform 180ms ease';
      box.style.transform = 'translate(0, 0)';
    });
    box.addEventListener('transitionend', function cleanup(event) {
      if (event.propertyName !== 'transform') {
        return;
      }
      box.style.transition = '';
      box.style.transform = '';
      box.removeEventListener('transitionend', cleanup);
    });
  }

  function focusPrompt(selection) {
    window.requestAnimationFrame(function () {
      prompt.focus({ preventScroll: true });
      const position = selection === 'start' ? 0 : prompt.value.length;
      prompt.setSelectionRange(position, position);
    });
  }

  function scrollComposerIntoView(targetIndex) {
    const target = targetIndex === null ? composerDock : articleForEditIndex(targetIndex);
    if (!target) {
      return;
    }
    const behavior = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth';
    target.scrollIntoView({ block: targetIndex === null ? 'nearest' : 'center', behavior });
  }

  function moveComposerTo(targetIndex, selection) {
    const firstRect = form.getBoundingClientRect();
    if (currentEditIndex === null) {
      dockPromptValue = prompt.value;
    }
    clearEditingArticle();

    if (targetIndex === null) {
      composerDock.insertBefore(form, composerEndTarget || null);
      currentEditIndex = null;
      prompt.value = dockPromptValue;
      originalPromptValue = dockPromptValue;
    } else {
      const article = articleForEditIndex(targetIndex);
      if (!article) {
        return;
      }
      article.dataset.editing = 'true';
      editSlotFor(article).append(form);
      currentEditIndex = targetIndex;
      originalPromptValue = promptTextForArticle(article);
      prompt.value = originalPromptValue;
    }

    syncPromptHeight();
    updatePromptHistoryState();
    updateComposerState();
    animateComposerFrom(firstRect);
    scrollComposerIntoView(targetIndex);
    focusPrompt(selection);
  }

  function moveComposerToDockForSubmit() {
    const firstRect = form.getBoundingClientRect();
    clearEditingArticle();
    composerDock.insertBefore(form, composerEndTarget || null);
    currentEditIndex = null;
    dockPromptValue = '';
    updatePromptHistoryState();
    animateComposerFrom(firstRect);
  }

  function createMessageActions(role) {
    if (role !== 'assistant') {
      return null;
    }
    const actions = document.createElement('div');
    actions.className = 'message-actions';
    actions.setAttribute('aria-label', 'Message actions');

    const copy = document.createElement('button');
    copy.className = 'message-action';
    copy.type = 'button';
    copy.dataset.copyMessage = '';
    copy.setAttribute('aria-label', 'Copy message');
    copy.title = 'Copy message';
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

  function createFinalStatus(statusData) {
    const text = typeof statusData?.text === 'string' ? statusData.text : '';
    if (!text) {
      return null;
    }
    const kind = typeof statusData.kind === 'string' && statusData.kind ? statusData.kind : 'status';
    const label = typeof statusData.label === 'string' && statusData.label ? statusData.label : kind;

    const status = document.createElement('div');
    status.className = 'message-status';
    status.dataset.statusKind = kind;
    status.dataset.statusState = 'complete';

    const toggle = document.createElement('button');
    toggle.className = 'message-status-toggle';
    toggle.type = 'button';
    toggle.dataset.statusToggle = '';
    toggle.textContent = label;
    toggle.setAttribute('aria-expanded', 'false');

    const content = document.createElement('div');
    content.className = 'message-status-content';
    content.id = typeof statusData.content_id === 'string' && statusData.content_id ? statusData.content_id : nextStatusContentID();
    content.hidden = true;
    content.textContent = text;

    toggle.setAttribute('aria-controls', content.id);
    status.append(toggle, content);
    return status;
  }

  function replaceFinalStatuses(article, statuses) {
    article.querySelectorAll('.message-status').forEach(function (status) {
      status.remove();
    });
    if (!Array.isArray(statuses)) {
      return;
    }
    const body = article.querySelector('.message-text');
    statuses.forEach(function (statusData) {
      const status = createFinalStatus(statusData);
      if (status) {
        article.insertBefore(status, body);
      }
    });
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
    const index = options && Number.isInteger(options.messageIndex) ? options.messageIndex : nextMessageIndex;
    article.dataset.messageIndex = String(index);
    nextMessageIndex = Math.max(nextMessageIndex, index + 1);
    if (role === 'user') {
      article.dataset.editablePrompt = 'true';
    }
    if (options && options.streaming) {
      article.classList.add('message-streaming');
    }

    const messageText = document.createElement('div');
    messageText.className = role === 'assistant' ? 'message-text markdown-body' : 'message-text message-plain';
    messageText.textContent = text || '';

    article.append(messageText);
    const actions = createMessageActions(role);
    if (actions) {
      article.append(actions);
    }
    insertMessage(article);
    scrollToBottom(true, true);
    return { article, text: messageText };
  }

  function truncateMessagesFrom(index) {
    const snapshot = {
      articles: [],
      nextMessageIndex,
    };
    messages.querySelectorAll('.message[data-message-index]').forEach(function (article) {
      if (messageIndex(article) >= index) {
        snapshot.articles.push(article);
        article.remove();
      }
    });
    nextMessageIndex = index;
    ensureEmptyState();
    updatePromptHistoryState();
    updateScrollButton();
    return snapshot;
  }

  function restoreTruncatedMessages(snapshot) {
    if (!snapshot) {
      return;
    }
    const empty = messages.querySelector('.message-empty');
    if (empty) {
      empty.remove();
    }
    snapshot.articles.forEach(function (article) {
      insertMessage(article);
    });
    nextMessageIndex = snapshot.nextMessageIndex;
    updatePromptHistoryState();
    updateScrollButton();
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

  function assistantOutputStarted(assistant) {
    return Boolean(assistant && (assistant.text.textContent || assistant.text.innerHTML));
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
    if (actionButton) {
      const state = currentTurn ? (abortRequested ? 'stopping' : 'stop') : 'send';
      const label = currentTurn ? (abortRequested ? 'Stopping response' : 'Stop response') : 'Send message';
      const iconName = currentTurn ? 'stop' : 'play';
      actionButton.dataset.actionState = state;
      actionButton.disabled = currentTurn ? abortRequested : creatingTurn || prompt.value.trim() === '';
      actionButton.setAttribute('aria-label', label);
      actionButton.title = label;
      actionIcons.forEach(function (icon) {
        icon.toggleAttribute('hidden', icon.dataset.actionIcon !== iconName);
      });
    }
    updateHistoryButtons(submitting);
    prompt.disabled = submitting;
  }

  function updateHistoryButtons(submitting) {
    const busy = submitting || Boolean(dirtyDialog && dirtyDialog.open);
    const previous = userMessageBefore(transcriptPosition());
    const canRedo = currentEditIndex !== null;
    if (undoButton) {
      undoButton.disabled = busy || !previous;
    }
    if (redoButton) {
      redoButton.disabled = busy || !canRedo;
    }
    if (ffwdButton) {
      ffwdButton.disabled = busy || currentEditIndex === null;
    }
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
    setStatus(status || '');
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

  function markTurnStopped(assistant) {
    if (!assistant) {
      return;
    }
    assistant.article.classList.remove('message-streaming');
    assistant.article.classList.add('message-stopped');
    completeThinkingStatus(assistant.article);
    if (!assistantHasContent(assistant)) {
      const status = assistant.article.querySelector('.message-status');
      if (status) {
        status.remove();
      }
    }
    let note = assistant.article.querySelector('.message-stopped-note');
    if (!note) {
      note = document.createElement('div');
      note.className = 'message-stopped-note';
      note.textContent = 'Stopped';
      const actions = assistant.article.querySelector('.message-actions');
      assistant.article.insertBefore(note, actions || null);
    }
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

  function enhanceMath(root) {
    if (typeof window.renderMathInElement !== 'function') {
      return;
    }
    window.renderMathInElement(root, {
      delimiters: [
        { left: '$$', right: '$$', display: true },
        { left: '\\[', right: '\\]', display: true },
        { left: '\\(', right: '\\)', display: false },
      ],
      ignoredTags: ['script', 'noscript', 'style', 'textarea', 'pre', 'code'],
      throwOnError: false,
    });
  }

  function mermaidThemeName() {
    return currentTheme() === 'dark' ? 'dark' : 'default';
  }

  function ensureMermaidInitialized() {
    if (!window.mermaid) {
      return;
    }
    const nextTheme = mermaidThemeName();
    if (mermaidInitialized && mermaidCurrentTheme === nextTheme) {
      return;
    }
    window.mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: nextTheme,
    });
    mermaidInitialized = true;
    mermaidCurrentTheme = nextTheme;
  }

  function refreshMermaidThemes() {
    if (!window.mermaid) {
      return;
    }
    const previousTheme = mermaidCurrentTheme;
    ensureMermaidInitialized();
    if (previousTheme === mermaidCurrentTheme) {
      return;
    }
    document.querySelectorAll('.code-block-mermaid').forEach(function (block) {
      const diagram = block.querySelector('.mermaid-render');
      if (diagram) {
        diagram.replaceChildren();
      }
      renderMermaidBlock(block);
    });
  }

  function enhanceMermaidBlocks(root) {
    root.querySelectorAll('.code-block').forEach(function (block) {
      if (block.dataset.mermaidEnhanced === 'true') {
        return;
      }
      const code = block.querySelector('code');
      if (languageFromCode(code).toLowerCase() !== 'mermaid') {
        return;
      }

      block.dataset.mermaidEnhanced = 'true';
      block.dataset.sourceVisible = 'false';
      block.classList.add('code-block-mermaid');

      const header = block.querySelector('.code-block-header');
      if (header) {
        const toggle = document.createElement('button');
        toggle.className = 'copy-code mermaid-toggle';
        toggle.type = 'button';
        toggle.dataset.mermaidToggle = '';
        toggle.textContent = 'Source';
        toggle.setAttribute('aria-label', 'Show Mermaid source');

        const retry = document.createElement('button');
        retry.className = 'copy-code mermaid-retry';
        retry.type = 'button';
        retry.dataset.mermaidRetry = '';
        retry.textContent = 'Retry';
        retry.setAttribute('aria-label', 'Retry Mermaid render');

        header.append(toggle, retry);
      }

      const diagram = document.createElement('div');
      diagram.className = 'mermaid-render';
      diagram.setAttribute('aria-live', 'polite');
      block.insertBefore(diagram, block.querySelector('pre'));
      renderMermaidBlock(block);
    });
  }

  async function renderMermaidBlock(block) {
    const code = block.querySelector('code');
    const diagram = block.querySelector('.mermaid-render');
    if (!code || !diagram) {
      return;
    }
    if (!window.mermaid) {
      diagram.textContent = 'Mermaid renderer unavailable.';
      block.dataset.mermaidState = 'unavailable';
      return;
    }

    ensureMermaidInitialized();
    const source = code.textContent || '';
    const id = `mermaid-${++mermaidIDCounter}`;
    diagram.textContent = 'Rendering diagram...';
    block.dataset.mermaidState = 'rendering';
    try {
      const result = await window.mermaid.render(id, source);
      diagram.innerHTML = result.svg || '';
      block.dataset.mermaidState = 'rendered';
    } catch (error) {
      diagram.textContent = 'Mermaid diagram could not be rendered.';
      block.dataset.mermaidState = 'error';
    }
  }

  function toggleMermaidSource(button) {
    const block = button.closest('.code-block-mermaid');
    if (!block) {
      return;
    }
    const showSource = block.dataset.sourceVisible !== 'true';
    block.dataset.sourceVisible = String(showSource);
    button.textContent = showSource ? 'Diagram' : 'Source';
    button.setAttribute('aria-label', showSource ? 'Show Mermaid diagram' : 'Show Mermaid source');
  }

  function enhanceMessage(article) {
    const body = article.querySelector('.markdown-body');
    if (body) {
      enhanceCodeBlocks(body);
      enhanceMermaidBlocks(body);
      enhanceMath(body);
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

  async function submitPrompt(text, options) {
    const body = { prompt: text };
    if (options && Number.isInteger(options.replaceFrom)) {
      body.replace_from = options.replaceFrom;
    }
    const response = await fetch('/chat/turns', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        [csrfHeaderName()]: csrfToken,
      },
      body: JSON.stringify(body),
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
    setStatus('');
    try {
      await abortTurn(turn);
    } catch (error) {
      if (currentTurn === turn) {
        abortRequested = false;
        updateComposerState();
        setStatus('');
      }
      throw error;
    }
  }

  function navigateTo(targetIndex, selection) {
    moveComposerTo(targetIndex, selection || (targetIndex === null ? 'end' : 'end'));
  }

  function continueNavigationAfterDiscard() {
    if (!pendingNavigation) {
      return;
    }
    const target = pendingNavigation;
    pendingNavigation = null;
    prompt.value = originalPromptValue;
    if (currentEditIndex === null) {
      dockPromptValue = originalPromptValue;
    }
    navigateTo(target.index, target.selection);
  }

  function requestNavigation(targetIndex, selection) {
    if (currentTurn || creatingTurn) {
      return;
    }
    if (hasDirtyPrompt()) {
      pendingNavigation = { index: targetIndex, selection };
      updateComposerState();
      if (dirtyDialog && typeof dirtyDialog.showModal === 'function') {
        dirtyDialog.showModal();
        updateComposerState();
        return;
      }
      if (window.confirm('Discard unsaved prompt changes?')) {
        continueNavigationAfterDiscard();
      } else {
        pendingNavigation = null;
      }
      updateComposerState();
      return;
    }
    navigateTo(targetIndex, selection);
  }

  function navigateBackward() {
    const previous = userMessageBefore(transcriptPosition());
    if (previous) {
      requestNavigation(messageIndex(previous), 'end');
    }
  }

  function navigateForward() {
    if (currentEditIndex === null) {
      return;
    }
    const next = userMessageAfter(currentEditIndex);
    requestNavigation(next ? messageIndex(next) : null, 'start');
  }

  function historyNavigationBusy() {
    return Boolean(currentTurn) || creatingTurn || Boolean(dirtyDialog && dirtyDialog.open);
  }

  function canNavigateBackward() {
    return !historyNavigationBusy() && Boolean(userMessageBefore(transcriptPosition()));
  }

  function canNavigateForward() {
    return !historyNavigationBusy() && currentEditIndex !== null;
  }

  function isUndoShortcut(event) {
    return (event.ctrlKey || event.metaKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'z';
  }

  function isRedoShortcut(event) {
    if (!(event.ctrlKey || event.metaKey) || event.altKey) {
      return false;
    }
    const key = event.key.toLowerCase();
    return (key === 'y' && !event.shiftKey) || (key === 'z' && event.shiftKey);
  }

  function handleHistoryShortcut(event) {
    if (event.isComposing || event.keyCode === 229) {
      return false;
    }
    if (isUndoShortcut(event) && canNavigateBackward()) {
      event.preventDefault();
      navigateBackward();
      return true;
    }
    if (isRedoShortcut(event) && canNavigateForward()) {
      event.preventDefault();
      navigateForward();
      return true;
    }
    return false;
  }

  function isEditablePromptInteractiveTarget(element) {
    return Boolean(element.closest('a, button, input, label, select, textarea, [contenteditable="true"]'));
  }

  function editablePromptArticle(element) {
    const article = element.closest('.message-user[data-editable-prompt="true"][data-message-index]');
    return article && messages.contains(article) ? article : null;
  }

  function handleEditablePromptMouseDown(event) {
    if (!(event.target instanceof Element) || isEditablePromptInteractiveTarget(event.target)) {
      return;
    }
    const article = editablePromptArticle(event.target);
    if (article) {
      event.preventDefault();
    }
  }

  function handleEditablePromptClick(event) {
    if (!(event.target instanceof Element) || isEditablePromptInteractiveTarget(event.target)) {
      return false;
    }
    const article = editablePromptArticle(event.target);
    if (!article) {
      return false;
    }
    const index = messageIndex(article);
    if (index < 0) {
      return false;
    }

    event.preventDefault();
    if (currentEditIndex === index) {
      focusPrompt('end');
      return true;
    }
    requestNavigation(index, 'end');
    return true;
  }

  function caretAtStart() {
    return prompt.selectionStart === 0 && prompt.selectionEnd === 0;
  }

  function caretAtEnd() {
    return prompt.selectionStart === prompt.value.length && prompt.selectionEnd === prompt.value.length;
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
      markTurnStopped(assistant);
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
      setStatus('');
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
      completeThinkingStatus(assistant.article);
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
      if (assistantOutputStarted(assistant)) {
        completeThinkingStatus(assistant.article);
      }
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
      if (Array.isArray(data.statuses)) {
        replaceFinalStatuses(assistant.article, data.statuses);
      } else {
        completeThinkingStatus(assistant.article);
      }
      assistant.article.classList.remove('message-streaming');
      assistant.article.classList.add('message-complete');
      if (!assistantHasContent(assistant)) {
        removeMessage(assistant);
      }
      scrollToBottom(false, wasNearBottom);
      finishTurn();
    });

    currentSource.addEventListener('aborted', function () {
      if (currentTurn !== turn) {
        return;
      }
      clearStreamErrorTimer();
      markTurnStopped(assistant);
      finishTurn('');
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

    const replaceFrom = currentEditIndex;
    moveComposerToDockForSubmit();
    let truncatedSnapshot = null;
    if (replaceFrom !== null) {
      truncatedSnapshot = truncateMessagesFrom(replaceFrom);
    }

    const userIndex = replaceFrom === null ? nextMessageIndex : replaceFrom;
    const user = addMessage('user', text, { messageIndex: userIndex });
    const assistant = addMessage('assistant', '', { streaming: true });
    currentUser = user;
    currentAssistant = assistant;
    prompt.value = '';
    originalPromptValue = '';
    syncPromptHeight();
    creatingTurn = true;
    updateComposerState();
    setStatus('Starting response');

    try {
      const turn = await submitPrompt(text, replaceFrom === null ? null : { replaceFrom });
      assignMessageIDs(user, assistant, turn);
      creatingTurn = false;
      subscribe(turn, user, assistant);
      updateComposerState();
    } catch (error) {
      creatingTurn = false;
      discardTurn(user, assistant);
      restoreTruncatedMessages(truncatedSnapshot);
      finishTurn('Message not sent');
    }
  });

  prompt.addEventListener('keydown', function (event) {
    if (event.isComposing || event.keyCode === 229) {
      return;
    }
    if (handleHistoryShortcut(event)) {
      return;
    }
    if (event.key === 'ArrowUp' && !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey && caretAtStart()) {
      event.preventDefault();
      navigateBackward();
      return;
    }
    if (event.key === 'ArrowDown' && !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey && caretAtEnd()) {
      event.preventDefault();
      navigateForward();
      return;
    }
    if (event.key !== 'Enter' || event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) {
      return;
    }
    event.preventDefault();
    if (!currentTurn) {
      form.requestSubmit();
    }
  });

  prompt.addEventListener('input', function () {
    if (currentEditIndex === null) {
      dockPromptValue = prompt.value;
    }
    syncPromptHeight();
    updateComposerState();
  });

  if (actionButton) {
    actionButton.addEventListener('click', async function () {
      if (!currentTurn) {
        form.requestSubmit();
        return;
      }
      const turn = currentTurn;
      const assistant = currentAssistant;
      try {
        await requestAbort(turn);
        if (currentTurn === turn) {
          markTurnStopped(assistant);
          finishTurn('');
        }
      } catch (error) {
        if (currentTurn === turn && assistant) {
          markTurnError(assistant, 'The turn could not be stopped.');
          finishTurn('Stop failed');
        }
      }
    });
  }

  if (undoButton) {
    undoButton.addEventListener('click', navigateBackward);
  }

  if (redoButton) {
    redoButton.addEventListener('click', navigateForward);
  }

  if (ffwdButton) {
    ffwdButton.addEventListener('click', function () {
      requestNavigation(null, 'end');
    });
  }

  if (composerEndTarget) {
    composerEndTarget.addEventListener('click', function () {
      requestNavigation(null, 'end');
    });
  }

  if (dirtyDialog) {
    dirtyDialog.addEventListener('close', function () {
      const action = dirtyDialog.returnValue;
      if (action === 'submit') {
        pendingNavigation = null;
        form.requestSubmit();
      } else if (action === 'discard') {
        continueNavigationAfterDiscard();
      } else {
        pendingNavigation = null;
      }
      dirtyDialog.returnValue = '';
      updateComposerState();
    });
  }

  document.addEventListener('keydown', function (event) {
    if (event.defaultPrevented || event.target === prompt) {
      return;
    }
    handleHistoryShortcut(event);
  });

  messages.addEventListener('mousedown', handleEditablePromptMouseDown);
  messages.addEventListener('scroll', updateScrollButton, { passive: true });

  if (scrollButton) {
    scrollButton.addEventListener('click', function () {
      scrollToBottom(true, true);
    });
  }

  if (themeToggle) {
    themeToggle.addEventListener('click', toggleTheme);
  }

  if (systemThemeQuery) {
    const onSystemThemeChange = function () {
      if (storedTheme()) {
        return;
      }
      applyThemePreference();
      refreshMermaidThemes();
    };
    if (typeof systemThemeQuery.addEventListener === 'function') {
      systemThemeQuery.addEventListener('change', onSystemThemeChange);
    } else if (typeof systemThemeQuery.addListener === 'function') {
      systemThemeQuery.addListener(onSystemThemeChange);
    }
  }

  document.addEventListener('click', async function (event) {
    if (!(event.target instanceof Element)) {
      return;
    }

    if (handleEditablePromptClick(event)) {
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

    const mermaidToggle = event.target.closest('[data-mermaid-toggle]');
    if (mermaidToggle) {
      toggleMermaidSource(mermaidToggle);
      return;
    }

    const mermaidRetry = event.target.closest('[data-mermaid-retry]');
    if (mermaidRetry) {
      const block = mermaidRetry.closest('.code-block-mermaid');
      if (block) {
        renderMermaidBlock(block);
      }
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

  applyThemePreference();
  enhanceAllMessages();
  syncPromptHeight();
  updatePromptHistoryState();
  updateComposerState();
  updateScrollButton();
})();
