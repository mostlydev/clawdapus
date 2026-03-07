(function () {
  const STATUS_CLASSES = ["status-healthy", "status-starting", "status-unhealthy", "status-unknown"];

  function classFor(status) {
    if (status === "healthy" || status === "running") return "status-healthy";
    if (status === "starting") return "status-starting";
    if (status === "unhealthy" || status === "stopped" || status === "dead" || status === "exited") return "status-unhealthy";
    return "status-unknown";
  }

  function updateStatusDot(dot, klass) {
    STATUS_CLASSES.forEach((item) => dot.classList.remove(item));
    dot.classList.add(klass);
  }

  function applyStatusPayload(payload) {
    if (!payload || !payload.services) return;
    Object.entries(payload.services).forEach(([service, status]) => {
      const liveStatus = status.status || "unknown";
      const klass = classFor(liveStatus);
      document.querySelectorAll(`[data-status-dot="${CSS.escape(service)}"]`).forEach((dot) => updateStatusDot(dot, klass));
      document.querySelectorAll(`[data-status-text="${CSS.escape(service)}"]`).forEach((node) => {
        node.textContent = liveStatus;
      });
      document.querySelectorAll(`[data-uptime-text="${CSS.escape(service)}"]`).forEach((node) => {
        node.textContent = status.uptime || "-";
      });
      document.querySelectorAll(`[data-container-id="${CSS.escape(service)}"]`).forEach((node) => {
        node.textContent = status.containerId || "-";
      });
      document.querySelectorAll(`[data-instance-running="${CSS.escape(service)}"]`).forEach((node) => {
        node.textContent = String(status.running || 0);
      });
      document.querySelectorAll(`[data-instance-total="${CSS.escape(service)}"]`).forEach((node) => {
        node.textContent = String(status.instances || 0);
      });
      document.querySelectorAll(`[data-token-status="${CSS.escape(service)}"]`).forEach((node) => {
        node.textContent = status.hasCllamaToken ? "present" : "absent";
      });
      document.querySelectorAll(`[data-service-card="${CSS.escape(service)}"]`).forEach((node) => {
        node.dataset.liveStatus = liveStatus.toLowerCase();
      });
    });
  }

  async function fetchStatus() {
    const res = await fetch("/api/status", { cache: "no-store" });
    if (!res.ok) throw new Error("status refresh failed");
    return res.json();
  }

  document.addEventListener("alpine:init", () => {
    Alpine.data("fleetPage", () => ({
      query: "",
      statusFilter: "all",
      async init() {
        await this.refresh();
        this.timer = window.setInterval(() => this.refresh(), 15000);
      },
      destroy() {
        if (this.timer) window.clearInterval(this.timer);
      },
      matches(el) {
        const q = this.query.trim().toLowerCase();
        const name = (el.dataset.serviceCard || "").toLowerCase();
        const role = (el.dataset.role || "").toLowerCase();
        const search = (el.dataset.search || "").toLowerCase();
        const liveStatus = (el.dataset.liveStatus || "").toLowerCase();
        const haystack = `${name} ${role} ${search}`;
        const statusPass = this.statusFilter === "all" || liveStatus === this.statusFilter;
        const queryPass = q === "" || haystack.includes(q);
        return statusPass && queryPass;
      },
      async refresh() {
        try {
          applyStatusPayload(await fetchStatus());
        } catch (_) {
          // keep prior state
        }
      }
    }));

    Alpine.data("detailPage", (service) => ({
      service,
      async init() {
        await this.refresh();
        this.timer = window.setInterval(() => this.refresh(), 15000);
      },
      destroy() {
        if (this.timer) window.clearInterval(this.timer);
      },
      async refresh() {
        try {
          const payload = await fetchStatus();
          if (!payload || !payload.services || !payload.services[this.service]) return;
          applyStatusPayload({ services: { [this.service]: payload.services[this.service] } });
        } catch (_) {
          // keep prior state
        }
      }
    }));

    Alpine.data("topologyPage", () => ({
      async init() {
        this.attachHover();
        await this.refresh();
        this.timer = window.setInterval(() => this.refresh(), 15000);
      },
      destroy() {
        if (this.timer) window.clearInterval(this.timer);
      },
      attachHover() {
        const stage = document.getElementById("topology-stage");
        if (!stage) return;
        const nodes = Array.from(stage.querySelectorAll("[data-node-id]"));
        const edges = Array.from(stage.querySelectorAll(".topology-edge"));
        const clearMute = () => {
          nodes.forEach((node) => node.classList.remove("muted"));
          edges.forEach((edge) => edge.classList.remove("muted"));
        };
        const muteUnrelated = (activeID, neighbors) => {
          const keep = new Set([activeID, ...neighbors]);
          nodes.forEach((node) => {
            if (!keep.has(node.dataset.nodeId)) node.classList.add("muted");
          });
          edges.forEach((edge) => {
            const from = edge.dataset.from;
            const to = edge.dataset.to;
            if (!(keep.has(from) && keep.has(to))) edge.classList.add("muted");
          });
        };
        nodes.forEach((node) => {
          node.addEventListener("mouseenter", () => {
            clearMute();
            const id = node.dataset.nodeId;
            const neighbors = (node.dataset.neighbors || "").split(",").filter(Boolean);
            muteUnrelated(id, neighbors);
          });
          node.addEventListener("mouseleave", clearMute);
        });
      },
      async refresh() {
        try {
          applyStatusPayload(await fetchStatus());
        } catch (_) {
          // keep prior state
        }
      }
    }));
  });
})();
