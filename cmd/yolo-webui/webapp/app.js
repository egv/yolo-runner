(function () {
  const { useState, useEffect } = React;
  const token = new URLSearchParams(window.location.search).get("token") || "";
  const authHeaders = () =>
    token ? { Authorization: "Bearer " + token } : {};

  const wsAuthSuffix = token ? "?token=" + encodeURIComponent(token) : "";
  const apiAuthSuffix = token ? "?token=" + encodeURIComponent(token) : "";

  const emptyState = {
    CurrentTask: "n/a",
    Phase: "n/a",
    LastOutputAge: "n/a",
    StatusSummary: "",
    Queue: [],
    TaskGraph: [],
    TaskDetails: [],
    WorkerSummaries: [],
    RunParams: [],
    Performance: [],
    History: [],
    Landing: [],
    Triage: [],
    PanelLines: [],
    StatusBar: [],
  };

  const taskIdFromQueueLine = (line) => {
    const match = String(line || "").match(/^-\s*([^\s]+)\s+-/);
    return match ? match[1] : "";
  };

  const requestJSON = (url, options) =>
    fetch(url, {
      ...options,
      headers: {
        "Content-Type": "application/json",
        ...(options.headers || {}),
      },
    }).then((response) => {
      if (!response.ok) {
        return response.text().then((body) => {
          throw new Error(body || ("HTTP " + response.status));
        });
      }
      return response.json();
    });

  const withAuth = (value) => ({
    ...value,
    headers: {
      ...authHeaders(),
      ...(value.headers || {}),
    },
  });

  function Panel({ title, children }) {
    return React.createElement(
      "section",
      { className: "panel" },
      React.createElement("h2", null, title),
      children
    );
  }

  function SummaryValue({ label, value }) {
    return React.createElement(
      "div",
      { className: "line" },
      React.createElement("strong", null, label + ": "),
      React.createElement("span", null, value || "n/a")
    );
  }

  function App() {
    const [snapshot, setSnapshot] = useState({ State: emptyState, Config: { Source: "" } });
    const [sourceInput, setSourceInput] = useState("");
    const [feedback, setFeedback] = useState("");
    const [controlTask, setControlTask] = useState("");
    const [controlStatus, setControlStatus] = useState("blocked");
    const [controlComment, setControlComment] = useState("");
    const [controlBackends, setControlBackends] = useState("");
    const [controlAuth, setControlAuth] = useState("");
    const [error, setError] = useState("");

    useEffect(() => {
      fetch("/api/config" + apiAuthSuffix, withAuth({ method: "GET" }))
        .then((response) => response.json())
        .then((config) => {
          setSourceInput((config && config.source) || "");
          setSnapshot((prev) => ({ ...prev, Config: config || {} }));
        })
        .catch(() => {});
    }, []);

    useEffect(() => {
      fetch("/api/state" + apiAuthSuffix, withAuth({ method: "GET" }))
        .then((response) => response.json())
        .then((next) => {
          const current = next || {};
          setSnapshot({
            State: current.state || current.State || emptyState,
            Config: current.config || current.Config || snapshot.Config,
          });
        })
        .catch((err) => setError(err.message || "state load failed"));
    }, []);

    useEffect(() => {
      const wsURL =
        (window.location.protocol === "https:" ? "wss://" : "ws://") +
        window.location.host +
        "/ws" +
        wsAuthSuffix;
      const socket = new WebSocket(wsURL);
      socket.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data);
          if (!payload) {
            return;
          }
          setSnapshot((prev) => ({
            State: payload.state || payload.State || emptyState,
            Config: payload.config || prev.Config || { Source: "" },
          }));
        } catch (err) {
          setError("websocket decode failed");
        }
      };
      socket.onopen = () => setError("");
      socket.onerror = () => setError("websocket error");
      socket.onclose = () => setError("websocket disconnected");
      return () => socket.close();
    }, []);

    const state = snapshot.State || emptyState;
    const config = snapshot.Config || { Source: "" };
    const queue = state.Queue || [];
    const workers = state.WorkerSummaries || [];
    const graph = state.TaskGraph || [];
    const statusBar = state.StatusBar || [];
    const runParams = state.RunParams || [];
    const performance = state.Performance || [];
    const executorDashboard = state.ExecutorDashboard || [];
    const landing = state.Landing || [];
    const triage = state.Triage || [];

    const saveSource = () => {
      requestJSON(
        "/api/control" + apiAuthSuffix,
        withAuth({
          method: "POST",
          body: JSON.stringify({
            action: "set-source",
            source: sourceInput.trim(),
          }),
        })
      )
        .then((res) => {
          if (res && typeof res.status === "string") {
            setFeedback("source updated");
            return;
          }
          setFeedback("source synced");
        })
        .catch((err) => setError(err.message || "unable to save source"));
    };

    const updateTaskStatus = (taskID, status) => {
      requestJSON(
        "/api/control" + apiAuthSuffix,
        withAuth({
          method: "POST",
          body: JSON.stringify({
            action: "set-task-status",
            task_id: taskID,
            status: status,
            comment: controlComment,
            backends: controlBackends
              .split(",")
              .map((value) => value.trim())
              .filter(Boolean),
            status_auth_token: controlAuth,
          }),
        })
      )
        .then((response) => setFeedback(response.status || "task update sent"))
        .catch((err) => setError(err.message || "task update failed"));
    };

    const onQueueRowClick = (entry) => {
      const taskID = taskIdFromQueueLine(entry);
      if (!taskID) {
        return;
      }
      setControlTask(taskID);
    };

    const queueList = queue.map((entry) => {
      const isSelected = (taskIdFromQueueLine(entry) || "") === controlTask;
      return React.createElement(
        "li",
        {
          className: "queue-item" + (isSelected ? " queue-item--active" : ""),
          key: entry,
        },
        React.createElement(
          "div",
          {
            className: "toolbar",
            style: { justifyContent: "space-between", alignItems: "flex-start" },
          },
          React.createElement("span", null, entry),
          React.createElement(
            "button",
            {
              type: "button",
              onClick: () => onQueueRowClick(entry),
            },
            "inspect"
          )
        ),
        React.createElement(
          "div",
          { className: "toolbar", style: { marginTop: "0.35rem", gap: "0.3rem" } },
          React.createElement("select", {
            value: controlStatus,
            onChange: (event) => setControlStatus(event.target.value),
          }, ["open", "in_progress", "blocked", "closed", "failed"].map((status) => React.createElement("option", { value: status, key: status }, status))),
          React.createElement(
            "button",
            {
              type: "button",
              onClick: () => {
                const taskID = taskIdFromQueueLine(entry);
                if (!taskID) {
                  return;
                }
                updateTaskStatus(taskID, controlStatus);
              },
            },
            "set status"
          )
        )
      );
    });

    const workerList = workers.map((worker) => {
      return React.createElement(
        "li",
        { key: worker.WorkerID },
        `${worker.WorkerID || "worker"} => ${worker.Task || "n/a"} (queue=${worker.QueuePos || 0}, priority=${worker.TaskPriority || 0})`
      );
    });

    return React.createElement(
      React.Fragment,
      null,
      React.createElement("header", { className: "header" },
        React.createElement("h1", null, "yolo-webui"),
        React.createElement("p", { className: "muted" }, "Remote control and monitoring via websocket and distributed event bus.")
      ),
      React.createElement("section", { className: "panel" },
        React.createElement("div", { className: "summary" },
          React.createElement(SummaryValue, { label: "Current Task", value: state.CurrentTask }),
          React.createElement(SummaryValue, { label: "Phase", value: state.Phase }),
          React.createElement(SummaryValue, { label: "Last Output Age", value: state.LastOutputAge }),
          React.createElement(SummaryValue, { label: "Status Summary", value: state.StatusSummary })
        ),
        React.createElement("div", { className: "summary", style: { marginTop: "0.6rem" } },
          React.createElement("div", { className: "line" },
            React.createElement("strong", null, "Source: "),
            React.createElement("span", null, config.Source || "all")
          ),
          React.createElement("div", { className: "line" },
            React.createElement("strong", null, "Workers: "),
            React.createElement("span", null, String(workers.length))
          ),
          React.createElement("div", { className: "line" },
            React.createElement("strong", null, "Queued Tasks: "),
            React.createElement("span", null, String(queue.length))
          ),
          React.createElement("div", { className: "line" },
            React.createElement("strong", null, "Socket: "),
            React.createElement("span", null, error ? "unavailable" : "live")
          )
        )
      ),
      React.createElement("section", { className: "layout" },
        React.createElement(Panel, { title: "Dependency Graph" },
          React.createElement("pre", { className: "detail-block" }, graph.join("\n") || "- no graph"),
          React.createElement("h3", null, "Task Details"),
          React.createElement("pre", { className: "detail-block" }, (state.TaskDetails || []).join("\n") || "- no task selected")
        ),
        React.createElement(Panel, { title: "Execution Queue" },
          React.createElement("ul", { className: "list" }, queueList.length ? queueList : [React.createElement("li", { className: "muted", key: "empty" }, "no queued tasks")]),
          React.createElement("h3", null, "Task Control"),
          React.createElement("div", { className: "toolbar", style: { marginTop: "0.35rem" } },
            React.createElement("input", {
              value: sourceInput,
              onChange: (event) => setSourceInput(event.target.value),
              placeholder: "monitor source filter",
              "aria-label": "source filter",
            }),
            React.createElement("button", { type: "button", onClick: saveSource }, "save source")
          )
        ),
        React.createElement(Panel, { title: "Executors & Controls" },
          React.createElement("h3", null, "Workers"),
          React.createElement("ul", { className: "list" }, workerList.length ? workerList : [React.createElement("li", { className: "muted", key: "empty" }, "no workers yet")]),
          React.createElement("h3", null, "Queue Actions"),
          React.createElement("div", { className: "toolbar", style: { marginTop: "0.35rem" } },
            React.createElement("input", {
              value: controlTask,
              onChange: (event) => setControlTask(event.target.value),
              placeholder: "task id",
              "aria-label": "task id",
            }),
            React.createElement("select", {
              value: controlStatus,
              onChange: (event) => setControlStatus(event.target.value),
            }, ["open", "in_progress", "blocked", "closed", "failed"].map((status) => React.createElement("option", { value: status, key: status }, status))),
            React.createElement("input", {
              value: controlComment,
              onChange: (event) => setControlComment(event.target.value),
              placeholder: "comment",
              "aria-label": "comment",
              style: { minWidth: "12rem" },
            })
          ),
          React.createElement("div", { className: "toolbar", style: { marginTop: "0.35rem" } },
            React.createElement("input", {
              value: controlBackends,
              onChange: (event) => setControlBackends(event.target.value),
              placeholder: "status backends (comma-separated)",
              "aria-label": "status backends",
              style: { minWidth: "18rem" },
            }),
            React.createElement("input", {
              type: "password",
              value: controlAuth,
              onChange: (event) => setControlAuth(event.target.value),
              placeholder: "status auth token",
              "aria-label": "status auth token",
            })
          ),
          React.createElement("div", { className: "toolbar", style: { marginTop: "0.5rem" } },
            React.createElement("button", {
              type: "button",
              onClick: () => updateTaskStatus(controlTask, controlStatus),
            }, "Update task status")
          ),
          feedback ? React.createElement("p", { className: "muted" }, feedback) : null,
          error ? React.createElement("p", { className: "muted" }, "error: " + error) : null
        )
      ),
      React.createElement("section", { className: "layout--half" },
        React.createElement(Panel, { title: "Panel Rows" },
          React.createElement("pre", { className: "detail-block" }, (state.PanelLines || []).map((line) => `${line.label || ""}`).join("\n") || "- no panels")
        ),
        React.createElement(Panel, { title: "Task History" },
          React.createElement("pre", { className: "detail-block" }, (state.History || []).join("\n") || "- no history")
        )
      ),
      React.createElement("section", { className: "layout--half" },
        React.createElement(Panel, { title: "Status Bar" },
          React.createElement("pre", { className: "detail-block" }, statusBar.join("\n") || "- no status rows")
        ),
        React.createElement(Panel, { title: "Run Parameters" },
          React.createElement("pre", { className: "detail-block" }, runParams.join("\n") || "- no run parameters")
        )
      ),
      React.createElement("section", { className: "layout--half" },
        React.createElement(Panel, { title: "Performance" },
          React.createElement("pre", { className: "detail-block" }, performance.join("\n") || "- no performance metrics")
        ),
        React.createElement(Panel, { title: "Executor Dashboard" },
          React.createElement("pre", { className: "detail-block" }, executorDashboard.join("\n") || "- no executor data")
        )
      ),
      React.createElement("section", { className: "layout--half" },
        React.createElement(Panel, { title: "Landing Queue" },
          React.createElement("pre", { className: "detail-block" }, landing.join("\n") || "- no landing tasks")
        ),
        React.createElement(Panel, { title: "Triage" },
          React.createElement("pre", { className: "detail-block" }, triage.join("\n") || "- no triage tasks")
        )
      )
    );
  }

  const root = ReactDOM.createRoot(document.getElementById("root"));
  root.render(React.createElement(App));
})();
