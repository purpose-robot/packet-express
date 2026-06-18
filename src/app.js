// Neighbor Explorer — app entry (bundled by Bun)
// Cytoscape + fcose come from npm now (instead of CDN globals).
// Lucide is provided by the inline shim in index.html (window.lucide);
// Open Props tokens are inlined in the <style> block.
import cytoscape from "cytoscape";
import fcose from "cytoscape-fcose";

cytoscape.use(fcose);

(() => {
  /* DOM shorthand */
  var $ = (id) => document.getElementById(id);

  /* ════════════════════════════════════════════
     DEVICE CLASS DEFINITIONS
  ════════════════════════════════════════════ */
  function resolveColor(v) {
    return getComputedStyle(document.documentElement)
      .getPropertyValue(v)
      .trim();
  }
  function resolveSize(v) {
    var el = document.createElement("div");
    el.style.cssText = `position:absolute;visibility:hidden;width:var(${v})`;
    document.documentElement.appendChild(el);
    var px = el.getBoundingClientRect().width;
    el.remove();
    return (
      px ||
      parseInt(
        getComputedStyle(document.documentElement).getPropertyValue(v),
        10,
      )
    );
  }
  var CLASSES = {
    core: {
      label: "Core",
      color: resolveColor("--color-core"),
      size: resolveSize("--size-node-core"),
      tier: 5,
    },
    wlc: {
      label: "WLC",
      color: resolveColor("--color-wlc"),
      size: resolveSize("--size-node-wlc"),
      tier: 4,
    },
    access: {
      label: "Access",
      color: resolveColor("--color-access"),
      size: resolveSize("--size-node-access"),
      tier: 3,
    },
    ap: {
      label: "Wireless",
      color: resolveColor("--color-ap"),
      size: resolveSize("--size-node-client"),
      tier: 1,
    },
    phone: {
      label: "VoIP Phone",
      color: resolveColor("--color-phone"),
      size: resolveSize("--size-node-client"),
      tier: 0,
    },
    unknown: {
      label: "Unknown",
      color: resolveColor("--color-unknown"),
      size: resolveSize("--size-node-access"),
      tier: 2,
    },
  };
  var ORDER = ["core", "access", "wlc", "ap", "phone", "unknown"];

  /* platform → category, matched by regex against the device platform string.
	   Order matters: first match wins. VoIP phones are matched by hostname (SEP…). */
  var PLATFORM_RULES = [
    { type: "core", re: /c9500/i },
    { type: "wlc", re: /c9800/i },
    { type: "ap", re: /c91|cw91|air/i },
    { type: "access", re: /c1000|c2960|c9200|c9300|ie-3000|c3560/i },
    { type: "phone", re: /ip phone/i },
  ];

  function classify(hostname, platform) {
    if (/^SEP/i.test(hostname || "")) return "phone";
    var p = platform || "";
    for (var i = 0; i < PLATFORM_RULES.length; i++) {
      if (PLATFORM_RULES[i].re.test(p)) return PLATFORM_RULES[i].type;
    }
    return "unknown";
  }
  function shortIface(s) {
    return (s || "")
      .replace(/TwentyFiveGigE/i, "Twe")
      .replace(/HundredGigE/i, "Hu")
      .replace(/FortyGigabitEthernet/i, "Fo")
      .replace(/TenGigabitEthernet/i, "Te")
      .replace(/GigabitEthernet/i, "Gi")
      .replace(/FastEthernet/i, "Fa");
  }

  /* ════════════════════════════════════════════
     OUTER STATE  (survives across imports)
  ════════════════════════════════════════════ */
  var cy = null;
  var STATE_KEY = "topology-state"; // saved filters / layout / selection
  var DATA_KEY = "topology-data"; // last imported JSON
  function txt(v) {
    return (v == null ? "" : String(v)).trim();
  }
  /* escape imported strings before they go into innerHTML / attribute contexts.
	   Topology data (hostnames, interfaces, …) is device-advertised, not trusted. */
  function esc(v) {
    return (v == null ? "" : String(v))
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  /* ════════════════════════════════════════════
     THEME  (works with or without imported data)
  ════════════════════════════════════════════ */
  function updateCyTheme() {
    if (!cy) return;
    var isDark =
      document.documentElement.getAttribute("data-theme") !== "light";
    var edgeColor = resolveColor("--cy-edge");
    var edgeOp = isDark ? 0.4 : 0.65;
    cy.batch(() => {
      cy.nodes().style({
        "border-color": resolveColor("--cy-node-border"),
        "text-outline-color": resolveColor("--cy-text-outline"),
        "text-background-color": resolveColor("--cy-text-bg"),
        "text-background-opacity":
          parseFloat(resolveColor("--cy-text-bg-op")) || 0.75,
        color: resolveColor("--cy-text-color"),
      });
    });
    // Edges are themed purely via the stylesheet — NO per-edge bypasses (those race
    // with the renderer and flash the stylesheet colour before the bypass paints).
    // Rebuild the base `edge` rule for the current theme; regular edges AND unset
    // uplinks both use it, so they always match. Highlighted uplinks carry the
    // `.bb-hi` class (static accent); toggling it is an atomic, flash-free repaint.
    var sheet = cy.style().json();
    sheet.forEach((r) => {
      var st = r.style || r.css;
      if (!st) return;
      if (r.selector === "edge") {
        st["line-color"] = edgeColor;
        st.opacity = edgeOp;
      } else if (r.selector === ".faded") {
        // dark fades toward black, so dimmed nodes need MORE opacity there to
        // stay visible; light fades toward white and needs less.
        st.opacity = isDark ? 0.25 : 0.5;
      }
    });
    cy.style().fromJson(sheet).update();
  }
  (() => {
    var btn = $("theme-toggle"),
      root = document.documentElement;
    function applyTheme(t) {
      root.setAttribute("data-theme", t);
      try {
        localStorage.setItem("topology-theme", t);
      } catch {}
      var isDark = t === "dark";
      btn.setAttribute(
        "data-tip",
        isDark ? "Switch to light theme" : "Switch to dark theme",
      );
      btn.setAttribute(
        "aria-label",
        isDark ? "Switch to light theme" : "Switch to dark theme",
      );
      var icon = btn.querySelector("[data-lucide]");
      if (icon) {
        icon.setAttribute("data-lucide", isDark ? "sun" : "moon");
        if (window.lucide) lucide.createIcons({ el: btn });
      }
      updateCyTheme();
    }
    applyTheme(localStorage.getItem("topology-theme") || "dark");
    btn.addEventListener("click", () => {
      applyTheme(root.getAttribute("data-theme") === "dark" ? "light" : "dark");
    });
  })();

  /* ════════════════════════════════════════════
     BUILD DATA MODEL  (imported JSON → nodes + deduped edges)
       neighbors[] : CDP adjacency  (builds the topology)
       devices[]   : per-host inventory (serial / firmware / platform / ip)
  ════════════════════════════════════════════ */

  // Pretty-print a location key ("berlin" -> "Berlin", "new_york" -> "New York")
  function prettyLoc(s) {
    return txt(s)
      .split(/[\s_-]+/)
      .filter(Boolean)
      .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
      .join(" ");
  }

  /* Topology JSON is an object keyed by location name, each site holding its own
	   `neighbors` (CDP adjacency) and `devices` (inventory) arrays:
	     { "<location>": { neighbors:[…], devices:[…] }, … }
	   Flatten to the internal { neighbors, devices } shape with `location`
	   stamped onto every row so the rest of the model is location-agnostic. */
  function flattenTopology(data) {
    var neighbors = [],
      devices = [];
    if (data && typeof data === "object") {
      Object.keys(data).forEach((key) => {
        var site = data[key];
        if (!site || typeof site !== "object") return;
        if (!Array.isArray(site.neighbors) && !Array.isArray(site.devices))
          return;
        var loc = prettyLoc(key);
        (site.neighbors || []).forEach((r) => {
          neighbors.push(Object.assign({ location: loc }, r));
        });
        (site.devices || []).forEach((d) => {
          devices.push(Object.assign({ location: loc }, d));
        });
      });
    }
    return { neighbors: neighbors, devices: devices };
  }

  function buildModel(data) {
    data = flattenTopology(data);
    var nodeMap = {}; // hostname -> node data
    var edgeMap = {}; // canonical key -> edge data
    function ensure(host) {
      if (!nodeMap[host])
        nodeMap[host] = {
          id: host,
          ip: null,
          platform: null,
          location: "",
          ifaces: {},
        };
      return nodeMap[host];
    }

    (data.neighbors || []).forEach((r) => {
      var lhost = txt(r.local_hostname),
        rhost = txt(r.remote_hostname);
      if (!lhost || !rhost) return;
      var lip = txt(r.local_ip_address),
        lif = txt(r.local_interface);
      var rip = txt(r.remote_ip_address),
        rif = txt(r.remote_interface);
      var rplat = txt(r.remote_platform),
        loc = txt(r.location);

      var ln_ = ensure(lhost);
      if (lip && !ln_.ip) ln_.ip = lip;
      if (loc && !ln_.location) ln_.location = loc;
      if (lif) ln_.ifaces[lif] = true;

      var rn_ = ensure(rhost);
      if (rip) rn_.ip = rip; // CDP reports the remote's platform/ip
      if (rplat) rn_.platform = rplat;
      if (loc && !rn_.location) rn_.location = loc;
      if (rif) rn_.ifaces[rif] = true;

      // canonical undirected key including interfaces (keeps parallel links distinct)
      var a = `${lhost}\u0001${lif}`,
        b = `${rhost}\u0001${rif}`;
      var key = a < b ? `${a}\u0002${b}` : `${b}\u0002${a}`;
      if (!edgeMap[key])
        edgeMap[key] = {
          source: lhost,
          sourceIf: lif,
          target: rhost,
          targetIf: rif,
        };
    });

    // per-device inventory enrichment (serial / firmware / authoritative platform).
    // Stacks are described by a `stack_members` array; a standalone switch normalizes to 1 member.
    var serialMap = {};
    (data.devices || []).forEach((dv) => {
      var h = txt(dv.hostname);
      if (!h) return;
      var members;
      // stacks are described by a `stack_members` array (each entry has an `id`)
      if (Array.isArray(dv.stack_members) && dv.stack_members.length) {
        members = dv.stack_members.map((m, i) => ({
          member: m.id != null ? m.id : i + 1,
          role: txt(m.role),
          serial_number: txt(m.serial_number) || "—",
          software_version: txt(m.software_version) || "—",
        }));
      } else {
        members = [
          {
            member: 1,
            role: "",
            serial_number: txt(dv.serial_number) || "—",
            software_version: txt(dv.software_version) || "—",
          },
        ];
      }
      var sers = members
        .map((m) => m.serial_number)
        .filter((s) => s && s !== "—");
      var vers = members
        .map((m) => m.software_version)
        .filter((v) => v && v !== "—");
      serialMap[h] = {
        members: members,
        serial: sers.length ? sers.join("; ") : "—",
        // stack members share one firmware version
        sw_version: vers.length ? vers[0] : "—",
      };
      var n = ensure(h);
      if (txt(dv.platform)) n.platform = txt(dv.platform); // device record is authoritative
      if (txt(dv.ip_address) && !n.ip) n.ip = txt(dv.ip_address);
      if (txt(dv.location) && !n.location) n.location = txt(dv.location);
    });

    // classify every node now platforms are merged
    Object.keys(nodeMap).forEach((h) => {
      var n = nodeMap[h];
      n.type = classify(h, n.platform);
      if (!n.platform) n.platform = "—";
      n.ifaceList = Object.keys(n.ifaces);
    });
    return { nodeMap: nodeMap, edgeMap: edgeMap, serialMap: serialMap };
  }

  /* choose tree-layout root(s) for arbitrary data: cores, else highest tier/degree */
  function pickRoots() {
    var cores = cy.nodes().filter((n) => n.data("type") === "core");
    if (cores.length) return cores;
    var best = null;
    cy.nodes().forEach((n) => {
      if (!best) {
        best = n;
        return;
      }
      var bt = CLASSES[best.data("type")].tier,
        nt = CLASSES[n.data("type")].tier;
      if (
        nt > bt ||
        (nt === bt && (n.data("deg") || 0) > (best.data("deg") || 0))
      )
        best = n;
    });
    return best ? best : cy.nodes();
  }

  /* ════════════════════════════════════════════
     BOOT  (build the entire app from one imported dataset)
  ════════════════════════════════════════════ */
  function boot(data) {
    var model = buildModel(data);
    var nodeMap = model.nodeMap,
      edgeMap = model.edgeMap,
      serialMap = model.serialMap;

    /* ════════════════════════════════════════════
     BUILD CYTOSCAPE ELEMENTS
  ════════════════════════════════════════════ */
    var elements = [];
    var degree = {};

    Object.keys(edgeMap).forEach((k) => {
      var e = edgeMap[k];
      degree[e.source] = (degree[e.source] || 0) + 1;
      degree[e.target] = (degree[e.target] || 0) + 1;
    });

    Object.keys(nodeMap).forEach((h) => {
      var n = nodeMap[h],
        cls = CLASSES[n.type];
      elements.push({
        group: "nodes",
        data: {
          id: h,
          label: h,
          type: n.type,
          color: cls.color,
          size: cls.size,
          ip: n.ip || "—",
          platform: n.platform,
          deg: degree[h] || 0,
          ifaces: n.ifaceList.map(shortIface).join(", "),
          location: n.location || "",
          serial: serialMap[h]?.serial || "—",
          sw_version: serialMap[h]?.sw_version || "—",
          members: serialMap[h]?.members || [],
        },
      });
    });

    var edgeId = 0;
    Object.keys(edgeMap).forEach((k) => {
      var e = edgeMap[k];
      var st = nodeMap[e.source].type,
        tt = nodeMap[e.target].type;
      var backbone = st === "core" || tt === "core";
      elements.push({
        group: "edges",
        data: {
          id: `e${edgeId++}`,
          source: e.source,
          target: e.target,
          sourceIf: shortIface(e.sourceIf),
          targetIf: shortIface(e.targetIf),
          kind: backbone ? "backbone" : "edge",
        },
      });
    });

    /* ════════════════════════════════════════════
     CYTOSCAPE INSTANCE
  ════════════════════════════════════════════ */
    cy = cytoscape({
      container: $("cy"),
      elements: elements,
      wheelSensitivity: 1,
      minZoom: 0.12,
      maxZoom: 3.5,
      style: [
        {
          selector: "node",
          style: {
            "background-color": "data(color)",
            width: "data(size)",
            height: "data(size)",
            "border-width": 2,
            "border-color": "#101219" /* wa gray-05 */,
            label: "data(label)",
            color: "#e4e5e9" /* wa gray-90 */,
            "font-family": "ui-monospace,Menlo,monospace",
            "font-size": 9,
            "font-weight": 500,
            "text-valign": "bottom",
            "text-halign": "center",
            "text-margin-y": 5,
            "text-outline-color": "#101219" /* wa gray-05 */,
            "text-outline-width": 2.4,
            "text-background-color": "#101219" /* wa gray-05 */,
            "text-background-opacity": 0.75,
            "text-background-padding": "2px",
            "text-background-shape": "roundrectangle",
            "text-max-width": 120,
            "min-zoomed-font-size": 7,
            "z-index": 10,
            "transition-property": "opacity,border-color,border-width",
            "transition-duration": "150ms",
          },
        },
        {
          selector: 'node[type="core"]',
          style: {
            label: "data(label)",
            "font-size": 11,
            "font-weight": 700,
            color: "#f1f2f3" /* wa gray-95 */,
            "border-width": 3,
            "border-color": "#101219" /* wa gray-05 */,
            "text-margin-y": 7,
          },
        },
        {
          selector: 'node[type="wlc"]',
          style: {
            "font-size": 10,
            "font-weight": 600,
            color: "#9da9ff" /* wa indigo-70 */,
            "text-margin-y": 6,
          },
        },
        {
          selector: 'node[type="ap"], node[type="phone"]',
          style: { "text-opacity": 0 },
        },
        { selector: ".showlabel", style: { "text-opacity": 1 } },

        {
          selector: "edge",
          style: {
            width: 1,
            "line-color": "#545868" /* wa gray-40 (themed via --cy-edge) */,
            opacity: 0.6,
            "curve-style": "bezier",
            "control-point-step-size": 18,
            "transition-property": "opacity",
            "transition-duration": "150ms",
          },
        },
        {
          selector: ".bb-hi",
          style: {
            width: 2.2,
            "line-color": "#808aff" /* wa indigo-60 */,
            opacity: 0.85,
          },
        },

        {
          selector: "node.sel",
          style: {
            "border-color": "#ffffff",
            "border-width": 3,
            "overlay-color": "data(color)",
            "overlay-opacity": 0.22,
            "overlay-padding": 6,
            "overlay-shape": "ellipse",
            "z-index": 99,
            "text-opacity": 1,
            color: "#ffffff",
            "font-weight": 700,
          },
        },
        {
          selector: "node:active",
          style: {
            "overlay-color": "data(color)",
            "overlay-opacity": 0.28,
            "overlay-padding": 6,
            "overlay-shape": "ellipse",
          },
        },
        {
          selector: "node.nbr",
          style: {
            "border-color": "#9da9ff" /* wa indigo-70 */,
            "border-width": 2.4,
            "text-opacity": 1,
            "z-index": 50,
          },
        },
        {
          selector: "edge.hi",
          style: {
            "line-color": "#9da9ff" /* wa indigo-70 */,
            opacity: 0.95,
            width: 2.6,
            "z-index": 90,
          },
        },
        {
          selector: ".faded",
          style: { opacity: 0.12, "text-opacity": 0 },
        },
      ],
    });

    /* ════════════════════════════════════════════
     PRECOMPUTED INDEXES (avoid repeated full-graph scans)
  ════════════════════════════════════════════ */
    var idxByPlatform = {},
      idxByFirmware = {},
      idxByType = {};
    cy.nodes().forEach((n) => {
      var p = n.data("platform");
      idxByPlatform[p] = idxByPlatform[p] || [];
      idxByPlatform[p].push(n);
      var f = n.data("sw_version");
      idxByFirmware[f] = idxByFirmware[f] || [];
      idxByFirmware[f].push(n);
      var ty = n.data("type");
      idxByType[ty] = idxByType[ty] || [];
      idxByType[ty].push(n);
    });

    /* ════════════════════════════════════════════
     LAYOUTS
  ════════════════════════════════════════════ */
    var LAYOUTS = {
      /* Force — organic spread; wide spacing keeps dense hub-and-spoke
			   sites (one core → many leaves) from piling up around each hub */
      fcose: {
        name: "fcose",
        quality: "proof",
        randomize: true,
        animate: true,
        animationDuration: 700,
        animationEasing: "ease-out",
        fit: true,
        padding: 60,
        nodeSeparation: 190,
        nodeRepulsion: 17000,
        idealEdgeLength: 150,
        edgeElasticity: 0.45,
        gravity: 0.15,
        gravityRange: 2.8,
        numIter: 3000,
        tile: true,
        tilingPaddingVertical: 24,
        tilingPaddingHorizontal: 24,
        nodeDimensionsIncludeLabels: true,
        packComponents: true,
      },
      /* Tree — top-down hierarchy rooted at the core */
      breadthfirst: {
        name: "breadthfirst",
        animate: true,
        animationDuration: 650,
        fit: true,
        padding: 60,
        directed: false,
        spacingFactor: 1.1,
        grid: true,
        circle: false,
        maximal: false,
        avoidOverlap: true,
        nodeDimensionsIncludeLabels: true,
      },
    };
    var curLayout = "breadthfirst";
    function runLayout(name, animate = true) {
      if (!LAYOUTS[name]) name = "breadthfirst"; // tree is the default layout
      curLayout = name;
      var vis = cy.nodes(":visible");
      var lo = (vis.length ? vis : cy.nodes()).union(cy.edges(":visible"));
      // breadthfirst's output depends on the nodes' starting positions. After a
      // visibility change (filtering on/off) nodes carry stale coordinates that
      // wedge the tree into a poor, overly tall shape that re-running Tree can't
      // escape — only switching to Force and back fixed it. Normalize with an
      // instant grid pre-pass so the tree is deterministic every time.
      if (name === "breadthfirst") {
        (vis.length ? vis : cy.nodes())
          .layout({ name: "grid", animate: false })
          .run();
      }
      var opts = Object.assign({}, LAYOUTS[name], { eles: lo });
      if (!animate) opts.animate = false; // instant on filter-driven re-runs
      if (name === "breadthfirst") {
        const roots = pickRoots().filter(":visible");
        if (roots.length) opts.roots = roots;
      }
      cy.layout(opts).run();
    }
    /* ════════════════════════════════════════════
     SELECTION + DETAIL PANEL
  ════════════════════════════════════════════ */
    var detail = $("detail");
    var selectedId = null;

    function clearSel() {
      var hadSelection = !!selectedId;
      selectedId = null;
      if (tempVisibleNode) {
        tempVisibleNode = null;
        applyFilters();
      }
      cy.elements().removeClass("sel nbr hi faded");
      detail.classList.remove("open");
      $("st-sel").textContent = "None";
      /* clear the search field/results that were set when focusing this node */
      if (hadSelection) {
        const se = $("search");
        if (se) se.value = "";
        const re = $("results");
        if (re) re.classList.remove("on");
      }
      if (typeof persistState === "function") persistState();
    }

    function selectNode(id, recenter) {
      var node = cy.getElementById(id);
      if (!node || node.empty()) return;
      // Un-hide previous temp node before switching
      if (tempVisibleNode && tempVisibleNode.id() !== id) {
        tempVisibleNode = null;
        applyFilters();
      }
      // Temporarily show filtered-out node
      if (node.style("display") === "none") {
        tempVisibleNode = node;
        node.style("display", "element");
      }
      selectedId = id;
      cy.elements().removeClass("sel nbr hi faded");

      var nhood = node.closedNeighborhood();
      cy.elements().not(nhood).addClass("faded");
      node.neighborhood("node").addClass("nbr");
      node.connectedEdges().addClass("hi");
      node.addClass("sel");

      $("st-sel").textContent = id;
      fillDetail(node);
      detail.classList.add("open");
      if (typeof persistState === "function") persistState();

      if (recenter) {
        const visNhood = node.closedNeighborhood().filter(":visible");
        const stageEl = $("stage");
        const panelW = 348,
          pad = 90;
        const sw =
          stageEl.clientWidth - panelW; /* usable width excluding panel */
        const sh = stageEl.clientHeight;
        const bb = visNhood.boundingBox({ includeLabels: false });
        if (bb.w === 0) bb.w = 1;
        if (bb.h === 0) bb.h = 1;
        let zoom = Math.min((sw - pad * 2) / bb.w, (sh - pad * 2) / bb.h, 2);
        zoom = Math.max(zoom, cy.minZoom());
        const panX = sw / 2 - zoom * (bb.x1 + bb.w / 2);
        const panY = sh / 2 - zoom * (bb.y1 + bb.h / 2);
        cy.animate(
          { zoom: zoom, pan: { x: panX, y: panY } },
          { duration: 380, easing: "ease-out-cubic" },
        );
      }
    }

    function fillDetail(node) {
      var d = node.data(),
        cls = CLASSES[d.type];
      $("d-dot").style.background = cls.color;
      var tl = $("d-tlabel");
      tl.textContent = cls.label;
      tl.style.color = cls.color;
      $("d-name").textContent = d.id;
      $("d-ip").textContent = d.ip;

      var grid = $("d-grid");
      var members = d.members || [];
      var html = cell(
        "Platform",
        d.platform ? d.platform.replace(/^cisco\s+/i, "") : "—",
        true,
      );
      var memHtml = "";
      if (members.length > 1) {
        memHtml = stackBlock(members);
      } else {
        var m0 = members[0] || {};
        html +=
          cell("Firmware", m0.software_version || d.sw_version, true, true) +
          cell("Serial Number", m0.serial_number || d.serial, true, true);
      }
      grid.innerHTML = html;
      $("d-members-section").innerHTML = memHtml;

      // neighbors
      var edges = node.connectedEdges();
      var rows = [];
      edges.forEach((e) => {
        var ed = e.data();
        var otherId = ed.source === d.id ? ed.target : ed.source;
        var myIf = ed.source === d.id ? ed.sourceIf : ed.targetIf;
        var theirIf = ed.source === d.id ? ed.targetIf : ed.sourceIf;
        var on = cy.getElementById(otherId).data();
        rows.push({
          id: otherId,
          type: on.type,
          color: CLASSES[on.type].color,
          myIf: myIf,
          theirIf: theirIf,
        });
      });
      rows.sort(
        (a, b) =>
          CLASSES[b.type].tier - CLASSES[a.type].tier ||
          a.id.localeCompare(b.id),
      );

      $("d-ncount").textContent =
        `${rows.length} link${rows.length !== 1 ? "s" : ""}`;
      var box = $("d-nbrs");
      box.innerHTML =
        rows
          .map(
            (r) =>
              '<div class="nbr" role="button" tabindex="0" data-go="' +
              esc(r.id) +
              '">' +
              '<span class="dot" style="background:' +
              r.color +
              '"></span>' +
              '<div class="nbr-main"><div class="nbr-name">' +
              esc(r.id) +
              "</div>" +
              '<div class="nbr-path"><em>' +
              esc(r.myIf) +
              "</em> → " +
              esc(r.theirIf) +
              "</div></div>" +
              '<i data-lucide="chevron-right" class="arr"></i></div>',
          )
          .join("") || '<div class="res-empty">No neighbors</div>';

      box.querySelectorAll(".nbr").forEach((el) => {
        function go() {
          selectNode(el.getAttribute("data-go"), true);
        }
        el.addEventListener("click", go);
        el.addEventListener("keydown", (e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            go();
          }
        });
      });
      if (window.lucide) lucide.createIcons({ el: box });
    }
    function cell(k, v, full, hideIfEmpty) {
      if (hideIfEmpty && (v == null || v === "" || v === "—")) return "";
      return (
        '<div class="d-cell' +
        (full ? " full" : "") +
        '"><div class="k">' +
        k +
        '</div><div class="v">' +
        esc(v) +
        "</div></div>"
      );
    }

    function stackBlock(members) {
      var body = members
        .map(
          (m) =>
            "<tr><td>" +
            esc(String(m.member != null ? m.member : "–")) +
            "</td><td>" +
            esc(m.role || "—") +
            '</td><td class="sm-mono">' +
            esc(m.serial_number || "—") +
            '</td><td class="sm-mono">' +
            esc(m.software_version || "—") +
            "</td></tr>",
        )
        .join("");
      return (
        '<div class="d-sub"><span class="bar"></span><span class="t">Members</span></div>' +
        '<div class="d-cell"><table class="stack-tbl"><thead><tr><th>#</th><th>Role</th><th>Serial</th><th>Firmware</th></tr></thead><tbody>' +
        body +
        "</tbody></table></div>"
      );
    }

    cy.on("tap", "node", (e) => {
      selectNode(e.target.id(), true);
    });
    cy.on("tap", (e) => {
      if (e.target === cy) clearSel();
    });
    $("dclose").addEventListener("click", clearSel);

    /* hover cursor */
    cy.on("mouseover", "node", () => {
      document.body.style.cursor = "pointer";
    });
    cy.on("mouseout", "node", () => {
      document.body.style.cursor = "default";
    });

    /* ════════════════════════════════════════════
     SIDEBAR: filters / legend
  ════════════════════════════════════════════ */
    var hiddenTypes = {};
    var selectedLoc = "";
    var locList = []; // every location in the dataset (single-location viewing only)
    var selectedPlatforms = new Set();
    var selectedFirmware = new Set();
    var tempVisibleNode = null;
    var toggleState = {}; // key -> current on/off (persisted)
    var toggleApply = {}; // key -> setter(state, persist)
    updateVisNote();

    /* Categories share the multi-select UI (built by makeMultiSelect below).
		   `hiddenTypes` remains the canonical store; this Set-like adapter lets the
		   factory toggle hidden categories through the same interface platform/
		   firmware use, so all downstream filter/persist logic is untouched. */
    var categorySel = {
      add(v) {
        hiddenTypes[v] = true;
      },
      delete(v) {
        delete hiddenTypes[v];
      },
      has(v) {
        return !!hiddenTypes[v];
      },
      get size() {
        return Object.keys(hiddenTypes).filter((k) => hiddenTypes[k]).length;
      },
    };
    function nodePassesExcept(n, excludeKey) {
      if (excludeKey !== "category" && hiddenTypes[n.data("type")])
        return false;
      if (selectedLoc && n.data("location") !== selectedLoc) return false;
      if (
        excludeKey !== "platform" &&
        selectedPlatforms.size > 0 &&
        selectedPlatforms.has(n.data("platform"))
      )
        return false;
      if (
        excludeKey !== "firmware" &&
        selectedFirmware.size > 0 &&
        selectedFirmware.has(n.data("sw_version")) &&
        n.data("sw_version") !== "—"
      )
        return false;
      return true;
    }
    function applyFilters() {
      var passing = new Set();
      cy.batch(() => {
        cy.nodes().forEach((n) => {
          var okType = !hiddenTypes[n.data("type")];
          var okLoc = !selectedLoc || n.data("location") === selectedLoc;
          var okPlat =
            selectedPlatforms.size === 0 ||
            !selectedPlatforms.has(n.data("platform"));
          var okFw =
            selectedFirmware.size === 0 ||
            !selectedFirmware.has(n.data("sw_version")) ||
            n.data("sw_version") === "—";
          var tempVis = tempVisibleNode && n.id() === tempVisibleNode.id();
          var show = (okType && okLoc && okPlat && okFw) || tempVis;
          if (show) passing.add(n.id());
          n.style("display", show ? "element" : "none");
        });
      });
      hideOrphans(passing);
      if (selectedId) {
        const s = cy.getElementById(selectedId);
        if (s.empty() || s.style("display") === "none") clearSel();
      }
      updateCounts();
    }
    // Hide nodes left with no path to a core root (e.g. an access switch whose only
    // uplink was filtered out). They'd otherwise float free of the topology — drop
    // them from the view entirely. `shown` is the authoritative set of node ids that
    // passed the filters this pass (read-back of display lags a tick behind the
    // batch). Skipped when no core is shown, and never hides a search-surfaced node.
    function hideOrphans(shown) {
      var roots = [];
      shown.forEach((id) => {
        if (cy.getElementById(id).data("type") === "core") roots.push(id);
      });
      if (!roots.length) return;
      // BFS from the roots over edges whose BOTH endpoints are shown.
      var reached = new Set(roots);
      var stack = roots.slice();
      while (stack.length) {
        var id = stack.pop();
        cy.getElementById(id)
          .connectedEdges()
          .forEach((e) => {
            var s = e.source().id(),
              t = e.target().id();
            if (!shown.has(s) || !shown.has(t)) return;
            var other = s === id ? t : s;
            if (!reached.has(other)) {
              reached.add(other);
              stack.push(other);
            }
          });
      }
      shown.forEach((id) => {
        if (reached.has(id)) return;
        if (tempVisibleNode && id === tempVisibleNode.id()) return;
        cy.getElementById(id).style("display", "none");
      });
    }
    /* keep every multi-select dropdown cross-filtered to the values still
		   reachable under the other active filters (category / platform / firmware) */
    function updateCounts() {
      if (window._syncCatDrop) window._syncCatDrop();
      if (window._syncPlatDrop) window._syncPlatDrop();
      if (window._syncFwDrop) window._syncFwDrop();
    }
    function updateVisNote() {
      var vis = cy.nodes(":visible").length;
      $("visnote").textContent = vis;
      var empty = $("empty-state");
      if (empty) empty.classList.toggle("show", vis === 0);
    }
    function updateResetBtn() {
      var active =
        Object.values(hiddenTypes).some(Boolean) ||
        selectedPlatforms.size > 0 ||
        selectedFirmware.size > 0;
      $("reset-filters").classList.toggle("visible", active);
    }

    /* ════════════════════════════════════════════
     STATE PERSISTENCE (filters / layout / selection)
  ════════════════════════════════════════════ */
    function persistState() {
      try {
        localStorage.setItem(
          STATE_KEY,
          JSON.stringify({
            hidden: Object.keys(hiddenTypes).filter((t) => hiddenTypes[t]),
            platforms: Array.from(selectedPlatforms),
            firmware: Array.from(selectedFirmware),
            loc: selectedLoc,
            layout: curLayout,
            toggles: toggleState,
            selected: selectedId,
          }),
        );
      } catch {}
    }
    function restoreState() {
      var raw;
      try {
        raw = JSON.parse(localStorage.getItem(STATE_KEY));
      } catch {
        return null;
      }
      if (!raw) return null;
      (raw.hidden || []).forEach((t) => {
        hiddenTypes[t] = true;
      });
      document.querySelectorAll("#category-drop .plat-item").forEach((item) => {
        if (hiddenTypes[item.getAttribute("data-cat")]) {
          item.classList.remove("on");
          const cb = item.querySelector("input");
          if (cb) cb.checked = false;
        }
      });
      // enforce single-location viewing: fall back to the first site if the
      // persisted location is empty or no longer present in this dataset
      var wantLoc =
        raw.loc && locList.indexOf(raw.loc) !== -1 ? raw.loc : locList[0] || "";
      if (wantLoc) {
        selectedLoc = wantLoc;
        const ls = $("loc-select");
        if (ls) ls.value = wantLoc;
        const sl = $("st-loc");
        if (sl) sl.textContent = wantLoc;
      }
      (raw.platforms || []).forEach((p) => {
        selectedPlatforms.add(p);
      });
      (raw.firmware || []).forEach((v) => {
        selectedFirmware.add(v);
      });
      document.querySelectorAll("#platform-drop .plat-item").forEach((item) => {
        if (selectedPlatforms.has(item.getAttribute("data-platform"))) {
          item.classList.remove("on");
          const cb = item.querySelector("input");
          if (cb) cb.checked = false;
        }
      });
      document.querySelectorAll("#firmware-drop .plat-item").forEach((item) => {
        if (selectedFirmware.has(item.getAttribute("data-fw"))) {
          item.classList.remove("on");
          const cb = item.querySelector("input");
          if (cb) cb.checked = false;
        }
      });
      if (window._syncCatDrop) window._syncCatDrop();
      if (window._syncPlatDrop) window._syncPlatDrop();
      if (window._syncFwDrop) window._syncFwDrop();
      if (raw.layout && LAYOUTS[raw.layout]) {
        curLayout = raw.layout;
        document.querySelectorAll("#layoutseg button").forEach((x) => {
          x.classList.toggle("on", x.getAttribute("data-lay") === raw.layout);
        });
      }
      return raw;
    }

    /* toggles */
    function bindToggle(key, def, handler) {
      var rowEl = document.querySelector(`[data-toggle="${key}"]`);
      var sw = $(`sw-${key}`);
      rowEl.setAttribute("role", "switch");
      rowEl.setAttribute("tabindex", "0");
      function apply(state, persist) {
        toggleState[key] = state;
        sw.classList.toggle("on", state);
        rowEl.setAttribute("aria-checked", state ? "true" : "false");
        handler(state);
        if (persist) persistState();
      }
      toggleApply[key] = apply;
      function flip() {
        apply(!toggleState[key], true);
      }
      rowEl.addEventListener("click", flip);
      rowEl.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          flip();
        }
      });
      apply(def, false); // establish default; restoreState may override later
    }
    bindToggle("alllabels", false, (on) => {
      if (on) cy.nodes('[type="ap"],[type="phone"]').addClass("showlabel");
      else cy.nodes().removeClass("showlabel");
    });
    bindToggle("backbone", true, (on) => {
      if (on) cy.edges('[kind="backbone"]').addClass("bb-hi");
      else cy.edges('[kind="backbone"]').removeClass("bb-hi");
    });

    /* layout segmented */
    document.querySelectorAll("#layoutseg button").forEach((b) => {
      b.addEventListener("click", () => {
        document.querySelectorAll("#layoutseg button").forEach((x) => {
          x.classList.remove("on");
        });
        b.classList.add("on");
        runLayout(b.getAttribute("data-lay"));
        persistState();
      });
    });

    /* zoom controls */
    $("zin").addEventListener("click", () => {
      cy.animate(
        {
          zoom: cy.zoom() * 1.35,
          center: { eles: cy.nodes(":visible") },
        },
        { duration: 180 },
      );
    });
    $("zout").addEventListener("click", () => {
      cy.animate(
        {
          zoom: cy.zoom() / 1.35,
          center: { eles: cy.nodes(":visible") },
        },
        { duration: 180 },
      );
    });
    $("zfit").addEventListener("click", () => {
      cy.animate({ fit: { padding: 55 } }, { duration: 300 });
    });

    /* ════════════════════════════════════════════
     SEARCH
  ════════════════════════════════════════════ */
    var searchEl = $("search");
    var resultsEl = $("results");
    var activeIdx = -1,
      curResults = [];

    function search(q) {
      q = q.trim().toLowerCase();
      if (!q) {
        resultsEl.classList.remove("on");
        return;
      }
      var out = [];
      cy.nodes().forEach((n) => {
        var d = n.data();
        // only suggest devices from the currently selected location
        if (selectedLoc && d.location !== selectedLoc) return;
        if (
          d.id.toLowerCase().indexOf(q) > -1 ||
          (d.ip && d.ip.toLowerCase().indexOf(q) > -1)
        ) {
          out.push(d);
        }
      });
      out.sort(
        (a, b) =>
          CLASSES[b.type].tier - CLASSES[a.type].tier ||
          a.id.localeCompare(b.id),
      );
      curResults = out.slice(0, 10);
      activeIdx = -1;
      renderResults();
    }
    function renderResults() {
      if (!curResults.length) {
        resultsEl.innerHTML = '<div class="res-empty">No matching device</div>';
        resultsEl.classList.add("on");
        return;
      }
      resultsEl.innerHTML = curResults
        .map(
          (d, i) =>
            '<div class="res-item' +
            (i === activeIdx ? " active" : "") +
            '" data-id="' +
            esc(d.id) +
            '">' +
            '<span class="res-dot" style="background:' +
            CLASSES[d.type].color +
            '"></span>' +
            '<span class="res-name">' +
            esc(d.id) +
            "</span>" +
            '<span class="res-meta">' +
            esc(d.ip) +
            "</span></div>",
        )
        .join("");
      resultsEl.classList.add("on");
      resultsEl.querySelectorAll(".res-item").forEach((el) => {
        el.addEventListener("click", () => {
          pick(el.getAttribute("data-id"));
        });
      });
    }
    function pick(id) {
      // un-hide that type if needed
      var t = cy.getElementById(id).data("type");
      if (hiddenTypes[t]) {
        hiddenTypes[t] = false;
        const item = document.querySelector(`#category-drop [data-cat="${t}"]`);
        if (item) {
          item.classList.add("on");
          const cb = item.querySelector("input");
          if (cb) cb.checked = true;
        }
        if (window._syncCatDrop) window._syncCatDrop();
        applyFilters();
        updateVisNote();
      }
      searchEl.value = id;
      resultsEl.classList.remove("on");
      selectNode(id, true);
    }
    searchEl.addEventListener("input", function () {
      search(this.value);
    });
    searchEl.addEventListener("keydown", (e) => {
      if (!curResults.length) return;
      if (e.key === "ArrowDown") {
        activeIdx = Math.min(activeIdx + 1, curResults.length - 1);
        renderResults();
        e.preventDefault();
      } else if (e.key === "ArrowUp") {
        activeIdx = Math.max(activeIdx - 1, 0);
        renderResults();
        e.preventDefault();
      } else if (e.key === "Enter") {
        pick(curResults[Math.max(activeIdx, 0)].id);
      } else if (e.key === "Escape") {
        resultsEl.classList.remove("on");
        searchEl.blur();
      }
    });
    document.addEventListener("click", (e) => {
      if (!e.target.closest(".search-wrap")) resultsEl.classList.remove("on");
    });
    /* keyboard shortcut: "/" or Cmd/Ctrl-K focuses search */
    document.addEventListener("keydown", (e) => {
      var isK = (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k";
      var isSlash = e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey;
      if (isK || isSlash) {
        const tag = (e.target.tagName || "").toLowerCase();
        if (tag === "input" || tag === "textarea" || tag === "select") return;
        e.preventDefault();
        searchEl.focus();
        searchEl.select();
      }
    });

    /* ════════════════════════════════════════════
     MULTI-SELECT FILTER FACTORY (Platform + Firmware)
  ════════════════════════════════════════════ */
    function makeMultiSelect(o) {
      /* o: {nodeAttr, selSet, dropId, btnId, labelId, sectionId, dataAttr, allLabel, fmt, syncName, index, excludeKey} */
      var values = Object.keys(o.index).filter((v) => v && v !== "—");
      values.sort(o.sort || ((a, b) => a.localeCompare(b)));
      var persist = o.persist || (() => {});

      if (values.length < 2) {
        $(o.sectionId).style.display = "none";
        return;
      }

      var drop = $(o.dropId);
      var btn = $(o.btnId);
      var lbl = $(o.labelId);
      var fmt = o.fmt || ((v) => v);

      btn.setAttribute("aria-haspopup", "true");
      btn.setAttribute("aria-expanded", "false");

      function setLabel(total) {
        var hidden = o.selSet.size;
        lbl.textContent =
          hidden === 0 ? o.allLabel : `${total - hidden} / ${total} selected`;
        btn.classList.toggle("active", hidden > 0);
      }

      values.forEach((v) => {
        var item = document.createElement("div");
        item.className = "plat-item on";
        item.setAttribute(o.dataAttr, v);
        item.innerHTML =
          '<input class="plat-cb" type="checkbox" checked>' +
          "<span>" +
          esc(fmt(v)) +
          "</span>";
        var cb = item.querySelector("input");
        cb.addEventListener("change", (e) => {
          e.stopPropagation();
          item.classList.toggle("on", cb.checked);
          if (!cb.checked) o.selSet.add(v);
          else o.selSet.delete(v);
          setLabel(values.length);
          applyFilters();
          updateVisNote();
          /* refresh BOTH dropdowns so the other one cross-filters to
					   only the values still reachable under this selection */
          if (window._syncCatDrop) window._syncCatDrop();
          if (window._syncPlatDrop) window._syncPlatDrop();
          if (window._syncFwDrop) window._syncFwDrop();
          updateResetBtn();
          persist();
          // re-run the active layout so the graph re-packs around the
          // nodes that remain visible after this filter change (no animation)
          runLayout(curLayout, false);
        });
        item.addEventListener("click", (e) => {
          if (e.target !== cb) {
            cb.checked = !cb.checked;
            cb.dispatchEvent(new Event("change"));
          }
        });
        drop.appendChild(item);
      });

      /* empty-state message shown when cross-filtering hides every option
			   (e.g. selected platforms — IP phones, APs — carry no firmware) */
      var emptyEl = null;
      if (o.emptyText) {
        emptyEl = document.createElement("div");
        emptyEl.className = "drop-empty";
        emptyEl.innerHTML = o.emptyText;
        drop.appendChild(emptyEl);
      }

      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        /* close any other open dropdown first */
        document.querySelectorAll(".platform-drop.open").forEach((d) => {
          if (d !== drop) {
            d.classList.remove("open");
            const pb = d.parentElement.querySelector(".platform-btn");
            if (pb) pb.setAttribute("aria-expanded", "false");
          }
        });
        var open = drop.classList.toggle("open");
        btn.setAttribute("aria-expanded", open ? "true" : "false");
      });
      document.addEventListener("click", () => {
        drop.classList.remove("open");
        btn.setAttribute("aria-expanded", "false");
      });
      drop.addEventListener("click", (e) => {
        e.stopPropagation();
      });

      /* sync: hide items whose nodes are all filtered out by EVERY other active
			   filter (category, location, and the OTHER multi-select). `excludeKey`
			   skips this dropdown's own filter so it never hides its own options. */
      window[o.syncName] = () => {
        drop.querySelectorAll(".plat-item").forEach((item) => {
          var v = item.getAttribute(o.dataAttr);
          var nodes = o.index[v] || [];
          var anyVisible = nodes.some((n) => nodePassesExcept(n, o.excludeKey));
          item.style.display = anyVisible ? "" : "none";
          if (!anyVisible && o.selSet.has(v)) {
            o.selSet.delete(v);
            const cb = item.querySelector("input");
            if (cb) cb.checked = true;
          }
        });
        var visTotal = values.filter((v) => {
          var el = drop.querySelector(`[${o.dataAttr}="${CSS.escape(v)}"]`);
          return el && el.style.display !== "none";
        }).length;
        if (emptyEl) emptyEl.classList.toggle("show", visTotal === 0);
        if (visTotal === 0 && o.emptyLabel) {
          lbl.textContent = o.emptyLabel;
          btn.classList.remove("active");
        } else {
          setLabel(visTotal);
        }
      };
    }

    makeMultiSelect({
      nodeAttr: "type",
      selSet: categorySel,
      index: idxByType,
      dropId: "category-drop",
      btnId: "category-btn",
      labelId: "category-label",
      sectionId: "category-section",
      dataAttr: "data-cat",
      allLabel: "All categories",
      fmt: (t) => (CLASSES[t] ? CLASSES[t].label : t),
      sort: (a, b) => ORDER.indexOf(a) - ORDER.indexOf(b),
      syncName: "_syncCatDrop",
      excludeKey: "category",
      emptyText: "No categories match the current filters.",
      emptyLabel: "No matches",
      persist: () => {
        persistState();
      },
    });

    makeMultiSelect({
      nodeAttr: "platform",
      selSet: selectedPlatforms,
      index: idxByPlatform,
      dropId: "platform-drop",
      btnId: "platform-btn",
      labelId: "platform-label",
      sectionId: "platform-section",
      dataAttr: "data-platform",
      allLabel: "All platforms",
      fmt: (p) => p.charAt(0).toUpperCase() + p.slice(1),
      syncName: "_syncPlatDrop",
      excludeKey: "platform",
      emptyText: "No platforms match the current filters.",
      emptyLabel: "No matches",
      persist: () => {
        persistState();
      },
    });

    makeMultiSelect({
      nodeAttr: "sw_version",
      selSet: selectedFirmware,
      index: idxByFirmware,
      dropId: "firmware-drop",
      btnId: "firmware-btn",
      labelId: "firmware-label",
      sectionId: "firmware-section",
      dataAttr: "data-fw",
      allLabel: "All versions",
      syncName: "_syncFwDrop",
      excludeKey: "firmware",
      emptyText: "No matching firmware found",
      emptyLabel: "No firmware",
      persist: () => {
        persistState();
      },
    });

    /* ════════════════════════════════════════════
     EDGE TOOLTIPS
  ════════════════════════════════════════════ */
    (() => {
      var tip = $("edge-tooltip");
      const stageEl = $("stage");

      function orderByTier(ed) {
        var sn = cy.getElementById(ed.source).data();
        var tn = cy.getElementById(ed.target).data();
        var sTier = CLASSES[sn.type] ? CLASSES[sn.type].tier : 0;
        var tTier = CLASSES[tn.type] ? CLASSES[tn.type].tier : 0;
        if (sTier >= tTier) {
          return [
            { host: ed.source, iface: ed.sourceIf },
            { host: ed.target, iface: ed.targetIf },
          ];
        } else {
          return [
            { host: ed.target, iface: ed.targetIf },
            { host: ed.source, iface: ed.sourceIf },
          ];
        }
      }

      function place(evt) {
        var r = stageEl.getBoundingClientRect();
        var x = evt.clientX - r.left + 14;
        var y = evt.clientY - r.top - 14;
        if (x + tip.offsetWidth > stageEl.clientWidth - 10)
          x = evt.clientX - r.left - tip.offsetWidth - 14;
        tip.style.left = `${x}px`;
        tip.style.top = `${y}px`;
      }

      cy.on("mouseover", "edge", (e) => {
        if (e.target.hasClass("faded")) return;
        var lines = orderByTier(e.target.data());
        tip.innerHTML =
          '<div class="tt-line">' +
          '<span class="tt-host">' +
          esc(lines[0].host) +
          ':</span><span class="tt-if">' +
          esc(lines[0].iface) +
          "</span>" +
          '<span style="color:var(--txt-mid);margin:0 var(--space-8)">&lt;-&gt;</span>' +
          '<span class="tt-host">' +
          esc(lines[1].host) +
          ':</span><span class="tt-if">' +
          esc(lines[1].iface) +
          "</span>" +
          "</div>";
        place(e.originalEvent);
        tip.style.display = "block";
      });
      cy.on("mousemove", "edge", (e) => {
        place(e.originalEvent);
      });
      cy.on("mouseout", "edge", () => {
        tip.style.display = "none";
      });
      cy.on("tap", () => {
        tip.style.display = "none";
      });
    })();

    /* (theme controller lives at the top of this IIFE so it also works before any import) */

    /* ════════════════════════════════════════════
     RESET FILTERS
  ════════════════════════════════════════════ */
    $("reset-filters").addEventListener("click", () => {
      // reset type filters
      Object.keys(hiddenTypes).forEach((t) => {
        hiddenTypes[t] = false;
      });
      document.querySelectorAll("#category-drop .plat-item").forEach((item) => {
        item.classList.add("on");
        var cb = item.querySelector("input");
        if (cb) cb.checked = true;
      });
      var catLbl = $("category-label");
      if (catLbl) catLbl.textContent = "All categories";
      $("category-btn").classList.remove("active");
      // reset firmware filters
      selectedFirmware.clear();
      document.querySelectorAll("#firmware-drop .plat-item").forEach((item) => {
        item.classList.add("on");
        var cb = item.querySelector("input");
        if (cb) cb.checked = true;
      });
      var fwLbl = $("firmware-label");
      if (fwLbl) fwLbl.textContent = "All versions";
      $("firmware-btn").classList.remove("active");
      // reset platform filters
      selectedPlatforms.clear();
      document.querySelectorAll("#platform-drop .plat-item").forEach((item) => {
        item.classList.add("on");
        var cb = item.querySelector("input");
        if (cb) cb.checked = true;
      });
      var lbl = $("platform-label");
      if (lbl) lbl.textContent = "All platforms";
      $("platform-btn").classList.remove("active");
      // apply
      applyFilters();
      updateVisNote();
      if (window._syncCatDrop) window._syncCatDrop();
      if (window._syncPlatDrop) window._syncPlatDrop();
      if (window._syncFwDrop) window._syncFwDrop();
      updateResetBtn();
      runLayout(curLayout);
      persistState();
    });

    /* ════════════════════════════════════════════
     LOCATION SELECTOR
  ════════════════════════════════════════════ */
    (() => {
      cy.nodes().forEach((n) => {
        var l = n.data("location");
        if (l && locList.indexOf(l) === -1) locList.push(l);
      });
      locList.sort();
      var sel = $("loc-select");
      locList.forEach((l) => {
        var o = document.createElement("option");
        o.value = l;
        o.textContent = l;
        sel.appendChild(o);
      });
      function setStLoc(v) {
        var e = $("st-loc");
        if (e) e.textContent = v || "—";
      }
      // always start on a concrete location — the app shows one site at a time
      if (locList.length >= 1) {
        selectedLoc = locList[0];
        sel.value = locList[0];
        setStLoc(locList[0]);
        $("loc-section").style.display = "";
      }
      sel.addEventListener("change", function () {
        // guard against an empty / unknown value — exactly one site is always shown
        if (!this.value || locList.indexOf(this.value) === -1) {
          this.value = selectedLoc;
          return;
        }
        selectedLoc = this.value;
        setStLoc(this.value);
        applyFilters();
        updateVisNote();
        if (window._syncPlatDrop) window._syncPlatDrop();
        if (window._syncFwDrop) window._syncFwDrop();
        /* re-layout + fit the now-visible site; otherwise the camera stays
				   parked over the previous location's cluster (often off-screen) */
        runLayout(curLayout);
        persistState();
      });
    })();

    /* ════════════════════════════════════════════
     EXPORT (PNG image / CSV / JSON of visible topology)
  ════════════════════════════════════════════ */
    (() => {
      var btn = $("exportbtn");
      var menu = $("export-menu");

      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        var open = menu.classList.toggle("open");
        btn.setAttribute("aria-expanded", open ? "true" : "false");
      });
      document.addEventListener("click", () => {
        menu.classList.remove("open");
        btn.setAttribute("aria-expanded", "false");
      });
      menu.addEventListener("click", (e) => {
        e.stopPropagation();
      });

      function dl(blob, name) {
        var url = URL.createObjectURL(blob);
        var a = document.createElement("a");
        a.href = url;
        a.download = name;
        a.click();
        setTimeout(() => {
          URL.revokeObjectURL(url);
        }, 2000);
      }
      function tag() {
        return (selectedLoc || "all").toLowerCase().replace(/[^a-z0-9]+/g, "-");
      }
      function visibleNodes() {
        return cy.nodes(":visible");
      }

      function exportPNG() {
        var bg = resolveColor("--bg-0") || "#101219"; /* wa gray-05 */
        var png = cy.png({
          output: "blob",
          bg: bg,
          full: true,
          scale: 2,
        });
        dl(png, `topology-${tag()}.png`);
      }
      function csvField(f) {
        f = String(f == null ? "" : f);
        return /[",\n]/.test(f) ? `"${f.replace(/"/g, '""')}"` : f;
      }
      function exportCSV() {
        var rows = [
          [
            "location",
            "hostname",
            "ip_address",
            "category",
            "platform",
            "member",
            "role",
            "serial_number",
            "firmware_version",
          ],
        ];
        visibleNodes().forEach((n) => {
          var d = n.data();
          // one row per stack member; devices without inventory emit a single row
          var mem =
            d.members && d.members.length
              ? d.members
              : [
                  {
                    member: "",
                    role: "",
                    serial_number: d.serial,
                    software_version: d.sw_version,
                  },
                ];
          mem.forEach((m) => {
            rows.push([
              d.location,
              d.id,
              d.ip,
              CLASSES[d.type].label,
              d.platform,
              m.member != null ? m.member : "",
              m.role || "",
              m.serial_number || "—",
              m.software_version || "—",
            ]);
          });
        });
        var csv = rows.map((r) => r.map(csvField).join(",")).join("\n");
        dl(
          new Blob([csv], { type: "text/csv;charset=utf-8" }),
          `topology-${tag()}.csv`,
        );
      }
      function exportJSON() {
        // re-emit the visible graph in the keyed-by-location topology format
        // so an export round-trips straight back through Import.
        var vis = visibleNodes(),
          ids = {},
          info = {};
        vis.forEach((n) => {
          ids[n.id()] = true;
          var d = n.data();
          info[n.id()] = {
            ip: d.ip || "",
            platform: d.platform || "",
            location: d.location || "",
          };
        });
        var sites = {};
        function site(loc) {
          var k = loc || "unknown";
          if (!sites[k]) sites[k] = { devices: [], neighbors: [] };
          return sites[k];
        }
        vis.forEach((n) => {
          var d = n.data();
          var dev = {
            platform: d.platform,
            hostname: d.id,
            ip_address: d.ip || "",
          };
          var mem = d.members || [];
          if (mem.length > 1) {
            dev.stack_members = mem.map((m) => ({
              id: m.member,
              role: m.role || "",
              serial_number: m.serial_number || "—",
              software_version: m.software_version || "—",
            }));
          } else {
            dev.serial_number = d.serial;
            dev.software_version = d.sw_version;
          }
          site(d.location).devices.push(dev);
        });
        cy.edges().forEach((e) => {
          var d = e.data();
          if (!ids[d.source] || !ids[d.target]) return;
          var src = info[d.source],
            tgt = info[d.target];
          site(src.location).neighbors.push({
            local_hostname: d.source,
            local_interface: d.sourceIf || "",
            local_ip_address: src.ip,
            remote_platform: tgt.platform,
            remote_hostname: d.target,
            remote_interface: d.targetIf || "",
            remote_ip_address: tgt.ip,
          });
        });
        dl(
          new Blob([JSON.stringify(sites, null, 2)], {
            type: "application/json",
          }),
          `topology-${tag()}.json`,
        );
      }

      menu.querySelectorAll(".export-opt").forEach((opt) => {
        opt.addEventListener("click", () => {
          menu.classList.remove("open");
          btn.setAttribute("aria-expanded", "false");
          var k = opt.getAttribute("data-export");
          if (k === "png") exportPNG();
          else if (k === "csv") exportCSV();
          else exportJSON();
        });
      });
    })();

    /* ════════════════════════════════════════════
     INIT  (per imported dataset)
  ════════════════════════════════════════════ */
    updateCyTheme(); // sync the freshly-built graph to the current theme
    var restored = restoreState();
    if (restored?.toggles) {
      Object.keys(restored.toggles).forEach((k) => {
        if (toggleApply[k]) toggleApply[k](restored.toggles[k], false);
      });
    }
    applyFilters();
    updateVisNote();
    updateResetBtn();
    if (window.lucide) lucide.createIcons();
    runLayout(curLayout);
    if (restored?.selected) {
      const rid = restored.selected;
      cy.one("layoutstop", () => {
        var n = cy.getElementById(rid);
        if (n && !n.empty() && n.style("display") !== "none")
          selectNode(rid, true);
      });
    }
    cy.ready(() => {
      setTimeout(() => {
        var l = $("loader");
        if (!l) return;
        l.classList.add("gone");
        setTimeout(() => {
          if (l) l.remove();
        }, 450);
      }, 750);
    });
  } /* ── end boot() ── */

  /* ════════════════════════════════════════════
     IMPORT  +  FIRST-RUN DISPATCH
  ════════════════════════════════════════════ */
  var appEl = $("app");
  var toastTimer = null;
  function showToast(msg) {
    var t = $("toast");
    if (!t) return;
    t.textContent = msg;
    t.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      t.classList.remove("show");
    }, 3400);
  }
  function showImportError(msg) {
    var e = $("import-err");
    if (e) e.textContent = msg || "";
    if (msg) showToast(msg);
  }
  function enterImportState() {
    appEl.classList.add("no-data");
    var l = $("loader");
    if (l) l.remove(); // no spinner on the empty screen
    if (window.lucide) lucide.createIcons();
  }
  function validModel(d) {
    return flattenTopology(d).neighbors.length > 0;
  }

  function handleFile(file) {
    if (!file) return;
    var reader = new FileReader();
    reader.onload = () => {
      var data;
      try {
        data = JSON.parse(reader.result);
      } catch {
        showImportError("That file isn't valid JSON.");
        return;
      }
      if (!validModel(data)) {
        showImportError(
          'Expected topology JSON: locations keyed by name, each holding a "neighbors" array.',
        );
        return;
      }
      try {
        localStorage.setItem(DATA_KEY, reader.result);
        localStorage.setItem(
          "topology-meta",
          JSON.stringify({ filename: file.name, importedAt: Date.now() }),
        );
        localStorage.removeItem(STATE_KEY); // start a new dataset with fresh filters
      } catch {}
      location.reload(); // clean rebuild from the saved data
    };
    reader.onerror = () => {
      showImportError("Couldn't read that file.");
    };
    reader.readAsText(file);
  }

  var fileInput = $("file-input");
  fileInput.addEventListener("change", function () {
    showImportError("");
    handleFile(this.files?.[0]);
    this.value = ""; // allow re-importing the same filename
  });
  function openPicker() {
    showImportError("");
    fileInput.click();
  }
  $("importbtn").addEventListener("click", openPicker);

  function populateFooter() {
    var el = $("ft-meta");
    if (!el) return;
    var meta = null;
    try {
      meta = JSON.parse(localStorage.getItem("topology-meta"));
    } catch {}
    if (meta && meta.filename) {
      var d = new Date(meta.importedAt);
      var p = (n) => String(n).padStart(2, "0");
      var ts =
        d.getFullYear() +
        "-" +
        p(d.getMonth() + 1) +
        "-" +
        p(d.getDate()) +
        " " +
        p(d.getHours()) +
        ":" +
        p(d.getMinutes());
      var safe = String(meta.filename).replace(
        /[&<>"]/g,
        (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c],
      );
      el.innerHTML =
        "<b>" +
        safe +
        '</b>&nbsp;<span class="ft-sep">·</span>&nbsp;imported ' +
        ts +
        '&nbsp;<span class="ft-sep">·</span>&nbsp;';
    } else {
      el.textContent = "";
    }
  }
  populateFooter();

  /* load the last import if present, otherwise show the import prompt */
  var savedRaw = null,
    savedData = null;
  try {
    savedRaw = localStorage.getItem(DATA_KEY);
  } catch {}
  if (savedRaw) {
    try {
      savedData = JSON.parse(savedRaw);
    } catch {
      savedData = null;
    }
  }
  if (validModel(savedData)) {
    appEl.classList.remove("no-data");
    boot(savedData);
  } else {
    enterImportState();
  }
})();
