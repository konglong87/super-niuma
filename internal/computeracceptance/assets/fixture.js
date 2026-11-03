"use strict";

(async () => {
  const TOKEN_HEADER = "X-Fixture-Token";
  const FLUSH_MS = 150;
  const REQUEST_TIMEOUT_MS = 3000;
  const LOG_LINES = 18;
  const EVENT_TYPES = [
    "click", "dblclick", "contextmenu", "mousemove", "mousedown", "mouseup",
    "wheel", "scroll", "keydown", "keyup", "beforeinput", "input",
    "compositionstart", "compositionupdate", "compositionend", "focus", "blur"
  ];
  const token = location.hash.slice(1);
  // Keep the capability only in memory, not in screenshot-visible URL/history.
  history.replaceState(history.state, "", location.pathname + location.search);
  const status = document.getElementById("connection");
  const encoder = new TextEncoder();
  const targets = [...document.querySelectorAll("[data-target]")];
  const input = document.getElementById("text-target");
  const scroll = document.getElementById("scroll-target");
  const dragSource = document.getElementById("drag-source");
  const dragDropTarget = document.getElementById("drag-drop-target");
  let dragActive = false;
  let dragCompleted = false;
  const queue = [];
  const log = [];
  let dropped = 0;
  let acknowledged = 0;
  let busy = false;
  let calibration = null;
  let geometryKey = "";

  async function request(path, options = {}) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
    try {
      const response = await fetch(path, {
        ...options, signal: controller.signal, cache: "no-store", credentials: "omit",
        headers: { [TOKEN_HEADER]: token, ...options.headers }
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      // Consume while the timeout is still active.
      return path === "/config" ? await response.json() : await response.text();
    } finally {
      clearTimeout(timeout);
    }
  }

  let limits;
  try {
    if (!/^[a-f0-9]{64}$/.test(token)) throw new Error("Open the full fixture URL, including its token fragment");
    limits = await request("/config");
  } catch (error) {
    status.textContent = `Not recording: ${error.message}`;
    return;
  }
  input.maxLength = Math.floor(limits.maxTextBytes / 4);

  function text(value, maxBytes = limits.maxTextBytes) {
    const source = String(value ?? "");
    if (encoder.encode(source).length <= maxBytes) return source;
    let result = "";
    let bytes = 0;
    for (const character of source) {
      bytes += encoder.encode(character).length;
      if (bytes > maxBytes) break;
      result += character;
    }
    return result;
  }

  function viewport() {
    const visual = window.visualViewport;
    return visual ? {
      offsetLeft: visual.offsetLeft, offsetTop: visual.offsetTop,
      pageLeft: visual.pageLeft, pageTop: visual.pageTop,
      width: visual.width, height: visual.height, scale: visual.scale
    } : null;
  }

  function windowGeometry() {
    return {
      windowScreen: { x: window.screenX, y: window.screenY },
      innerWidth, innerHeight, outerWidth, outerHeight, devicePixelRatio,
      pageScroll: { x: scrollX, y: scrollY },
      screenWidth: screen.width, screenHeight: screen.height,
      availableScreenOriginKnown: Number.isFinite(screen.availLeft) && Number.isFinite(screen.availTop),
      availableScreen: {
        x: screen.availLeft ?? 0, y: screen.availTop ?? 0,
        width: screen.availWidth, height: screen.availHeight
      },
      visualViewport: viewport()
    };
  }

  function currentGeometry() {
    const geometry = windowGeometry();
    const key = JSON.stringify(geometry);
    if (key !== geometryKey) calibration = null;
    geometryKey = key;
    return {
      ...geometry, calibration,
      targets: targets.map(target => {
        const rect = target.getBoundingClientRect();
        return {
          id: target.id,
          rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
          center: { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 }
        };
      })
    };
  }

  function calibrate(event) {
    if (!event.isTrusted || !(event instanceof MouseEvent)) return;
    // Keyboard-activated clicks have no pointing-device coordinate pair.
    if ((event.type === "click" && event.detail === 0) || event.type === "contextmenu") return;
    currentGeometry(); // Invalidate stale geometry before accepting a new pair.
    calibration = {
      client: { x: event.clientX, y: event.clientY },
      screen: { x: event.screenX, y: event.screenY }, timeStamp: event.timeStamp
    };
  }

  function pageState() {
    const value = text(input.value);
    return {
      inputValue: value, inputTruncated: value !== input.value,
      selectionStart: input.selectionStart ?? 0, selectionEnd: input.selectionEnd ?? 0,
      scrollTop: scroll.scrollTop, scrollLeft: scroll.scrollLeft,
      dragCompleted
    };
  }

  function eventRecord(event, target) {
    const position = event instanceof MouseEvent;
    const value = target === input ? text(input.value) : "";
    const data = text(event.data);
    return {
      type: event.type, target: target.id, isTrusted: event.isTrusted, timeStamp: event.timeStamp,
      position, client: { x: position ? event.clientX : 0, y: position ? event.clientY : 0 },
      screen: { x: position ? event.screenX : 0, y: position ? event.screenY : 0 },
      button: event.button ?? 0, buttons: event.buttons ?? 0, detail: event.detail ?? 0,
      key: text(event.key, limits.maxLabelBytes), code: text(event.code, limits.maxLabelBytes),
      modifiers: { alt: !!event.altKey, ctrl: !!event.ctrlKey, meta: !!event.metaKey, shift: !!event.shiftKey },
      repeat: !!event.repeat, isComposing: !!event.isComposing,
      inputType: text(event.inputType, limits.maxLabelBytes), data, value,
      truncated: data !== String(event.data ?? "") || (target === input && value !== input.value),
      scrollTop: target.scrollTop, scrollLeft: target.scrollLeft,
      deltaX: event.deltaX ?? 0, deltaY: event.deltaY ?? 0, deltaMode: event.deltaMode ?? 0
    };
  }

  function observe(event, target) {
    calibrate(event);
    const record = eventRecord(event, target);
    if (queue.length >= limits.maxQueueEvents) {
      queue.shift();
      dropped++;
    }
    queue.push(record);
    log.unshift(`${record.type} @ ${record.target} isTrusted=${record.isTrusted}` +
      ` key=${record.key} value=${record.value.slice(0, 80)}` +
      (record.position ? ` client=(${record.client.x},${record.client.y}) screen=(${record.screen.x},${record.screen.y})` : ""));
    log.length = Math.min(log.length, LOG_LINES);
    document.getElementById("event-log").textContent = log.join("\n");
    // Suppress only the context menu overlay; never manufacture an action.
    if (event.type === "contextmenu") event.preventDefault();
  }

  function renderGeometry(geometry, state) {
    const { targets: coordinates, ...summary } = geometry;
    document.getElementById("geometry").textContent = JSON.stringify(summary, null, 2);
    document.getElementById("input-state").textContent =
      `Selection: ${state.selectionStart}–${state.selectionEnd} · scrollTop: ${state.scrollTop} · value: ${state.inputValue}`;
    const rows = coordinates.map(target => {
      const row = document.createElement("tr");
      let estimate = "unknown — move the mouse";
      if (geometry.calibration) {
        const pair = geometry.calibration;
        estimate = `${(target.center.x + pair.screen.x - pair.client.x).toFixed(1)}, ` +
          `${(target.center.y + pair.screen.y - pair.client.y).toFixed(1)} (estimate)`;
      }
      for (const value of [target.id, `${target.center.x.toFixed(1)}, ${target.center.y.toFixed(1)}`, estimate]) {
        const cell = document.createElement("td");
        cell.textContent = value;
        row.append(cell);
      }
      return row;
    });
    document.getElementById("coordinates").replaceChildren(...rows);
  }

  function nextReport() {
    const geometry = currentGeometry();
    const state = pageState();
    renderGeometry(geometry, state);
    const report = { events: queue.splice(0, limits.maxBatchEvents), geometry, state, clientDropped: dropped };
    let body = JSON.stringify(report);
    while (encoder.encode(body).length > limits.maxRequestBytes && report.events.length > 1) {
      queue.unshift(report.events.pop());
      body = JSON.stringify(report);
    }
    return { report, body };
  }

  async function flush() {
    if (busy) return;
    busy = true;
    let batchSize = 0;
    try {
      const { report, body } = nextReport();
      batchSize = report.events.length;
      await request("/events", { method: "POST", headers: { "Content-Type": "application/json" }, body });
      acknowledged += batchSize;
      status.textContent = `Recording · acknowledged ${acknowledged} · queued ${queue.length} · client dropped/uncertain ${dropped}`;
    } catch (error) {
      // Never retry an ambiguously delivered event and accidentally count it twice.
      dropped += batchSize;
      status.textContent = `Report failed (${error.message}) · dropped/uncertain ${dropped}`;
    } finally {
      busy = false;
    }
  }

  dragSource.addEventListener("mousedown", event => {
    if (event.isTrusted && event.button === 0) dragActive = true;
  }, { capture: true });
  dragDropTarget.addEventListener("mouseup", event => {
    if (event.isTrusted && event.button === 0 && dragActive) {
      dragActive = false;
      dragCompleted = true;
      dragDropTarget.dataset.dragComplete = "true";
    }
  }, { capture: true });
  document.addEventListener("mouseup", event => {
    if (event.isTrusted && event.button === 0 && event.target !== dragDropTarget) dragActive = false;
  }, { capture: true });

  for (const target of targets) {
    for (const type of EVENT_TYPES) {
      target.addEventListener(type, event => observe(event, target), { capture: true, passive: type === "wheel" });
    }
  }
  document.addEventListener("mousemove", calibrate, { passive: true });
  const timer = setInterval(flush, FLUSH_MS);
  window.addEventListener("pagehide", () => clearInterval(timer), { once: true });
  await flush();
})();
