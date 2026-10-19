/*
 * MCP Apps bridge (shared by the mcp-diary widgets).
 *
 * Runs inside a sandboxed iframe. Talks JSON-RPC over postMessage with the
 * host: receives tool input/results, can call MCP tools back and can ask for
 * more space. All data is untrusted; render it with textContent only.
 */
(function () {
  "use strict";

  var pending = new Map();
  var nextID = 1;
  var listeners = { input: [], result: [], toolResult: [], size: [] };
  var latestInput = null;
  var latestResult = null;

  function api() {
    return typeof window !== "undefined" ? window.openai : undefined;
  }

  function request(method, params) {
    var id = nextID++;
    window.parent.postMessage({ jsonrpc: "2.0", id: id, method: method, params: params || {} }, "*");
    return new Promise(function (resolve, reject) {
      pending.set(id, { resolve: resolve, reject: reject });
    });
  }

  function emit(kind, value) {
    listeners[kind].forEach(function (fn) {
      try {
        fn(value);
      } catch (err) {
        console.error("widget handler failed", err);
      }
    });
  }

  window.addEventListener(
    "message",
    function (event) {
      var message = event.data;
      if (!message || message.jsonrpc !== "2.0") return;

      if (message.id !== undefined && pending.has(message.id)) {
        var p = pending.get(message.id);
        pending.delete(message.id);
        if (message.error) p.reject(message.error);
        else p.resolve(message.result);
        return;
      }

      if (message.method === "ui/notifications/tool-input") {
        latestInput = (message.params && message.params.arguments) || {};
        emit("input", latestInput);
      } else if (message.method === "ui/notifications/tool-result") {
        latestResult = message.params || {};
        emit("result", latestResult);
        emit("toolResult", latestResult);
      } else if (message.method === "ui/notifications/tool-cancelled") {
        emit("toolResult", { cancelled: true, reason: message.params && message.params.reason });
      } else if (message.method === "ui/notifications/host-context-changed") {
        applyHostContext(message.params);
      }
    },
    { passive: true }
  );

  function applyHostContext(params) {
    if (!params) return;
    var root = document.documentElement;
    if (params.theme) root.setAttribute("data-theme", params.theme);
    if (params.styles && params.styles.variables) {
      Object.keys(params.styles.variables).forEach(function (name) {
        if (name.indexOf("--") === 0) {
          root.style.setProperty(name, String(params.styles.variables[name]));
        }
      });
    }
    var height = params.containerDimensions && params.containerDimensions.maxHeight;
    if (height) emit("size", height);
  }

  function start() {
    request("ui/initialize", {
      appInfo: { name: "mcp-diary", version: "1" },
      appCapabilities: { availableDisplayModes: ["inline", "fullscreen"] },
      protocolVersion: "2026-01-26",
    })
      .then(function (result) {
        window.parent.postMessage({ jsonrpc: "2.0", method: "ui/notifications/initialized" }, "*");
        applyHostContext(result && result.hostContext);
      })
      .catch(function (err) {
        console.error("ui/initialize failed", err);
      });

    // ChatGPT compatibility path: the host may hand us the data directly.
    var o = api();
    if (o) {
      if (o.toolOutput) {
        latestResult = { structuredContent: o.toolOutput };
        emit("result", latestResult);
      }
      if (o.toolInput) {
        latestInput = o.toolInput;
        emit("input", latestInput);
      }
    }
  }

  window.diary = {
    onInput: function (fn) {
      listeners.input.push(fn);
      if (latestInput) fn(latestInput);
    },
    onSize: function (fn) {
      listeners.size.push(fn);
    },
    // onResult is the initial tool result only: the data the model called the
    // render tool with. Later results (pagination, deletes) arrive through
    // onToolResult so they never clobber the host's first push.
    onResult: function (fn) {
      listeners.result.push(fn);
      if (latestResult) fn(latestResult);
    },
    // onToolResult also receives results of tools/call requests made by the
    // view, and cancellations ({cancelled: true, reason}). Prefer the promise
    // returned by callTool and treat this as a notification stream.
    onToolResult: function (fn) {
      listeners.toolResult.push(fn);
      if (latestResult) fn(latestResult);
    },
    input: function () {
      return latestInput;
    },
    // callTool resolves with the raw tools/call result. mcp-diary tools wrap
    // their payload in {code, message, data}; see unwrap.
    callTool: function (name, args) {
      var o = api();
      var call;
      if (o && typeof o.callTool === "function") {
        // window.openai.callTool takes a single options object and may return
        // the unwrapped structuredContent, so only trust it when it does.
        call = Promise.resolve(o.callTool({ name: name, arguments: args || {} })).then(function (res) {
          if (res && res.structuredContent !== undefined) return res;
          if (res && (res.code !== undefined || res.isError)) return { structuredContent: res };
          return res;
        });
      } else {
        call = request("tools/call", { name: name, arguments: args || {} });
      }
      return call.then(function (res) {
        if (res && !res.cancelled && res.isError) {
          var err = new Error(errorText(res) || "工具调用失败");
          err.code = resultCode(res);
          return Promise.reject(err);
        }
        return res;
      });
    },
    // updateModelContext tells the host what changed so the model stays in
    // sync with the view. Hosts without support resolve to undefined.
    updateModelContext: function (content, structuredContent) {
      var params = { content: content || [] };
      if (structuredContent) params.structuredContent = structuredContent;
      return request("ui/update-model-context", params).catch(function (err) {
        console.warn("ui/update-model-context unsupported", err);
        return null;
      });
    },
    openLink: function (url) {
      var o = api();
      if (o && typeof o.openExternal === "function") {
        o.openExternal({ href: url });
        return;
      }
      request("ui/open-link", { url: url });
    },
    requestDisplayMode: function (mode) {
      var o = api();
      if (o && typeof o.requestDisplayMode === "function") {
        return Promise.resolve(o.requestDisplayMode({ mode: mode }));
      }
      return request("ui/request-display-mode", { mode: mode });
    },
    sendMessage: function (text) {
      var o = api();
      if (o && typeof o.sendFollowUpMessage === "function") {
        return Promise.resolve(o.sendFollowUpMessage({ prompt: text }));
      }
      return request("ui/message", {
        role: "user",
        content: [{ type: "text", text: text }],
      });
    },
  };

  function uuid() {
    if (typeof crypto !== "undefined" && crypto.randomUUID) return crypto.randomUUID();
    return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, function (c) {
      var r = (Math.random() * 16) | 0;
      var v = c === "x" ? r : (r & 0x3) | 0x8;
      return v.toString(16);
    });
  }

  // unwrap validates the mcp-diary envelope ({code, message, data}) and
  // rejects with the server message on failure.
  function unwrap(result) {
    var payload = result;
    if (result && result.structuredContent !== undefined) payload = result.structuredContent;
    if (payload && typeof payload === "object" && "code" in payload) {
      if (payload.code !== 0) {
        var err = new Error(payload.message || "request failed");
        err.code = payload.code;
        return Promise.reject(err);
      }
      return Promise.resolve(payload.data);
    }
    return Promise.resolve(payload);
  }

  // parseResult normalizes the shapes hosts use to deliver a tool result:
  // the MCP result object, its structuredContent, an OpenAI-style
  // toolOutput, or a content[] block carrying JSON text.
  function parseResult(result) {
    if (result && result.cancelled) return null;
    var payload = result;
    if (result && result.structuredContent !== undefined) payload = result.structuredContent;
    if (
      payload &&
      Array.isArray(payload.content) &&
      (payload.entries === undefined || payload.code === undefined)
    ) {
      for (var i = 0; i < payload.content.length; i++) {
        var block = payload.content[i];
        if (block && block.type === "text" && typeof block.text === "string") {
          try {
            var parsed = JSON.parse(block.text);
            if (parsed && typeof parsed === "object") return parsed;
          } catch (e) {
            // Not JSON: ignore this block.
          }
        }
      }
    }
    return payload || null;
  }

  // sequence guards async work: each call to next() invalidates the previous
  // one, so a slow response cannot overwrite a newer render.
  function sequence() {
    var current = 0;
    return {
      next: function () {
        current += 1;
        var issued = current;
        return function stale() {
          return issued !== current;
        };
      },
      cancel: function () {
        current += 1;
      },
    };
  }

  // resultCode extracts the mcp-diary error code from a tool result, so the
  // view can tell "already deleted" (404) from "changed elsewhere" (409).
  function resultCode(result) {
    var payload = result;
    if (result && result.structuredContent !== undefined) payload = result.structuredContent;
    return payload && payload.code !== undefined ? payload.code : undefined;
  }

  // errorText extracts the human-readable message from an error tool result.
  function errorText(result) {
    var payload = result;
    if (result && result.structuredContent !== undefined) payload = result.structuredContent;
    if (!payload) return "";
    if (payload.message) return payload.message;
    if (Array.isArray(result.content)) {
      for (var i = 0; i < result.content.length; i++) {
        if (result.content[i] && result.content[i].text) return String(result.content[i].text);
      }
    }
    return "";
  }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  }

  function formatDate(iso) {
    if (!iso) return "";
    var parts = String(iso).split("-");
    if (parts.length !== 3) return String(iso);
    var d = new Date(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]));
    var locale = document.documentElement.lang || navigator.language || "zh-CN";
    try {
      return d.toLocaleDateString(locale, { year: "numeric", month: "long", day: "numeric", weekday: "long" });
    } catch (e) {
      return String(iso);
    }
  }

  function snippet(text, max) {
    var flat = String(text || "").replace(/\s+/g, " ").trim();
    return flat.length > max ? flat.slice(0, max) + "…" : flat;
  }

  window.diaryBridge = {
    uuid: uuid,
    unwrap: unwrap,
    parseResult: parseResult,
    sequence: sequence,
    resultCode: resultCode,
    errorText: errorText,
    el: el,
    formatDate: formatDate,
    snippet: snippet,
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else {
    start();
  }
})();
