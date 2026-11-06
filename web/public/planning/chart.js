"use strict";
(() => {
  const resolutions = {
    1: "1m",
    5: "5m",
    15: "15m",
    60: "1h",
    240: "4h",
    "1D": "1d",
  };
  const toBar = (c) => ({
    time: c.time * 1000,
    open: Number(c.open),
    high: Number(c.high),
    low: Number(c.low),
    close: Number(c.close),
    volume: Number(c.volume),
  });
  const ticker = (v, s) => v.toUpperCase() + ":" + s;
  function instrument(name) {
    const at=name.indexOf(":"), venue=name.slice(0,at), symbol=name.slice(at+1);
    if (
      !venue ||
      !symbol ||
      !/^(?:[a-z0-9]{1,16}:)?[A-Z0-9\p{Script=Han}-]{1,40}$/u.test(symbol) ||
      !["binance", "okx", "bitget", "hyperliquid"].includes(venue.toLowerCase())
    )
      throw new Error("Invalid symbol format");
    return { venue: venue.toLowerCase(), symbol };
  }
  class WorkspaceChart {
    constructor({ venue, symbol, resolution = "60", onStatus, onPrice, onContext, onError, onAvailability, onAnnotations }) {
      this.venue = venue;
      this.symbol = symbol;
      this.resolution = resolutions[resolution] ? resolution : "60";
      this.onStatus = onStatus;
      this.onPrice = onPrice;
      this.onContext = onContext;
      this.onError = onError;
      this.onAvailability = onAvailability || (() => {});
      this.connections = new Map();
      this.lastTimes = new Map();
      this.barTimes = new Map();
      this.priceData = new Map();
      this.candleData = new Map();
      this.needsAutoScale = false;
      this.overlays = [];
      this.comparisonPlans=[];
      this.aiOverlays=[];this.aiVersion=0;this.aiRequest=0;this.aiDrawings=[];this.aiCandidates=[];this.expandedAI=false;this.showAI=true;
      this.onAnnotations=onAnnotations || (()=>{});
      this.plotVersion = 0;
      this.planKey = "";
      this.paused = false;
      this.disposed = false;
      this.lastReceived = 0;
      this.datafeed = this.makeDatafeed();
      this.ready = new Promise((resolve) => {
        this.readyResolve = resolve;
      });
      if (!window.TradingView?.widget) {
        this.loadError = "TradingView did not load. Restart the updated terminal and try again.";
        this.onAvailability(false, this.loadError);
        this.readyResolve();
        this.onError(this.loadError);
        return;
      }
      this.readyTimer = setTimeout(() => {
        this.loadError = "TradingView timed out. Check the market connection and restart the updated terminal.";
        this.onAvailability(false, this.loadError);
        this.readyResolve();
      }, 20000);
      this.widget = new TradingView.widget({
        container: "chart-container",
        library_path: "/charting_library/",
        symbol: ticker(venue, symbol),
        interval: this.resolution,
        locale: "en",
        timezone: "Etc/UTC",
        theme: "light",
        autosize: true,
        datafeed: this.datafeed,
        enabled_features: ["iframe_loading_same_origin"],
        disabled_features: [
          // The pinned v30.2 runtime gates its sampled analytics loader behind
          // this internal feature. Keep local chart sessions off that network.
          "14851",
          "header_symbol_search",
          "header_compare",
          "header_saveload",
          "use_localstorage_for_settings",
          "save_chart_properties_to_local_storage",
        ],
        overrides: {
          "paneProperties.backgroundType": "solid",
          "paneProperties.background": "#FFFFFF",
          "paneProperties.vertGridProperties.color": "#eef0eb",
          "paneProperties.horzGridProperties.color": "#eef0eb",
          "scalesProperties.textColor": "#7a8275",
          "mainSeriesProperties.candleStyle.upColor": "#2E8B57",
          "mainSeriesProperties.candleStyle.downColor": "#D6433A",
          "mainSeriesProperties.candleStyle.borderUpColor": "#2E8B57",
          "mainSeriesProperties.candleStyle.borderDownColor": "#D6433A",
          "mainSeriesProperties.candleStyle.wickUpColor": "#2E8B57",
          "mainSeriesProperties.candleStyle.wickDownColor": "#D6433A",
        },
        loading_screen: {
          backgroundColor: "#FFFFFF",
          foregroundColor: "#2E8B57",
        },
        auto_save_delay: 1,
      });
      this.widget.onChartReady(async () => {
        clearTimeout(this.readyTimer);
        if (this.disposed) return;
        this.chart = this.widget.activeChart();
        this.chart.onIntervalChanged().subscribe(null, (r) => {
          if (resolutions[r]) {
            const changed = this.resolution !== r;
            this.resolution = r;
            if (changed) {
              this.clearAI();
              this.onContext(this.context());
              this.redrawPlan();
            }
          }
        });
        this.widget.subscribe("onAutoSaveNeeded", () => this.saveLayout(false));
        this.chart.onDataLoaded?.().subscribe(null, () => {if(this.needsAutoScale) this.restoreAutoScale();this.redrawPlan();this.restorePrice();});
        this.chart.onVisibleRangeChanged?.().subscribe(null, range => this.queueComparisonViewport(range));
        this.readyResolve();
        try {
          const raw = localStorage.getItem(this.layoutKey());
          if (raw && raw.length < 2e6) await this.widget.load(JSON.parse(raw));
          this.restoreAutoScale();
        } catch {
          this.onError("Could not restore the saved chart layout. Save the current layout again.");
        }
      });
      this.ageTimer = setInterval(() => {
        if (
          !this.paused &&
          this.lastReceived &&
          Date.now() - this.lastReceived > 15000
        )
          this.onStatus("Market updates interrupted · stale data", true);
      }, 1000);
    }
    context() {
      let visible;
      // The native series can temporarily have no time scale during load,
      // interval changes or pane resizing. An unavailable viewport is explicit
      // (the host then reads the documented default range), never an old symbol.
      try {
        if (!this.disposed && (!this.chart?.dataReady || this.chart.dataReady())) {
          const range=this.chart?.getVisibleRange?.();
          if(range && Number.isFinite(range.from) && Number.isFinite(range.to) && range.to>range.from) {
            // Native viewport includes future whitespace, which is not market data.
            const from=Math.max(0,Math.floor(range.from)),to=Math.min(Math.floor(range.to),Math.floor(Date.now()/1000)+1);
            if(to>from) visible={from,to};
          }
        }
      } catch { /* native time scale is not ready yet */ }
      return {
        venue: this.venue,
        symbol: this.symbol,
        interval: resolutions[this.resolution],
        resolution: this.resolution,
        ...(visible ? {visible_from:Math.floor(visible.from),visible_to:Math.floor(visible.to)} : {}),
      };
    }
    async whenReady() {
      await this.ready;
      if (!this.chart || this.disposed) throw new Error(this.loadError || "Chart is not ready yet. Try again.");
    }
    queueComparisonViewport(range) {
      if(this.disposed || this.comparisonPlans?.length<2)return;
      try {range=range || this.chart?.getVisibleRange?.();}catch{return;}
      if(!range || !Number.isFinite(range.from) || !Number.isFinite(range.to) || range.to<=range.from)return;
      const key=[this.venue,this.symbol,this.resolution,Math.round(range.from),Math.round(range.to)].join(":");
      // Shape updates and price ticks can emit an unchanged native viewport.
      // Rebuilding its shapes again would create a redraw feedback loop.
      if(key===this.comparisonViewportKey)return;
      this.comparisonViewportKey=key;
      clearTimeout(this.comparisonViewportTimer);
      this.comparisonViewportTimer=setTimeout(()=>{if(!this.disposed && this.comparisonPlans?.length>=2)this.redrawPlan();},180);
    }
    redrawPlan() {
      if (this.changingResolution) return;
      if (!this.plan) return;
      this.planKey = "";
      this.plot(this.plan, this.planPhase, this.comparisonPlans);
    }
    layoutKey() {
      return "nofx.planning.chart.v1.perpetual." + this.venue + "." + this.symbol;
    }
    async json(path) {
      const response = await fetch(path, { credentials: "same-origin" });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "Market data request failed");
      return data;
    }
    query(info, resolution, extra = {}) {
      const { venue, symbol } = instrument(info.ticker || info.name);
      return new URLSearchParams({
        venue,
        symbol,
        interval: resolutions[resolution],
        ...extra,
      }).toString();
    }
    // Native endpoints cap pages at 300; countBack may span multiple pages.
    async history(info, resolution, to, count) {
      const rows = new Map();
      let cursor = Math.min(to, Math.floor(Date.now()/1000)+1), exhausted = false, metadata = {};
      for (let page = 0; page < 16 && rows.size < count; page++) {
        const data = await this.json("/api/planning/candles?" + this.query(info, resolution, {
          limit: String(Math.min(300, count - rows.size)), to: String(cursor),
        }));
        if (!page) metadata = data;
        const batch = (data.candles || []).filter(r => r.time < cursor).sort((a,b) => a.time-b.time);
        if (!batch.length) { exhausted = true; break; }
        for (const row of batch) rows.set(row.time, row);
        cursor = batch[0].time;
      }
      return { ...metadata, candles: [...rows.values()].sort((a,b) => a.time-b.time), exhausted };
    }
    rememberCandles(key, rows) {
      if(!this.candleData.has(key) && this.candleData.size>=24) this.candleData.delete(this.candleData.keys().next().value);
      const merged=new Map((this.candleData.get(key) || []).map(r=>[r.time,r]));
      for(const row of rows) merged.set(row.time,row);
      this.candleData.set(key,[...merged.values()].sort((a,b)=>a.time-b.time).slice(-4800));
    }
    restoreAutoScale() {
      this.needsAutoScale = true;
      if(this.chart?.dataReady && !this.chart.dataReady()) return;
      this.chart?.getPanes?.()[0]?.getMainSourcePriceScale()?.setAutoScale(true);
      this.needsAutoScale = false;
    }
    makeDatafeed() {
      return {
        onReady: (cb) =>
          setTimeout(
            () =>
              cb({
                supported_resolutions: Object.keys(resolutions),
                supports_time: false,
                exchanges: [],
                symbols_types: [],
              }),
            0,
          ),
        searchSymbols: (_input, _exchange, _type, cb) =>
          setTimeout(() => cb([]), 0),
        resolveSymbol: async (name, cb, fail) => {
          try {
            const { venue, symbol } = instrument(name);
            let minmov = 1,
              pricescale = 100000000;
            try {
              const quote = await this.json(
                "/api/planning/instrument?" + new URLSearchParams({ venue, symbol }),
              );
              const precision = (
                String(quote.tick).replace(/0+$/, "").split(".")[1] || ""
              ).length;
              pricescale = 10 ** precision;
              minmov = Math.max(1, Math.round(Number(quote.tick) * pricescale));
            } catch {
              /* Axis precision is display-only. Executable plans always validate native rules separately. */
            }
            cb({
              name,
              ticker: name,
              description: symbol + " · " + venue.toUpperCase(),
              type: "crypto",
              session: "24x7",
              timezone: "Etc/UTC",
              exchange: venue.toUpperCase(),
              listed_exchange: venue.toUpperCase(),
              minmov,
              pricescale,
              has_intraday: true,
              has_daily: true,
              intraday_multipliers: ["1", "5", "15", "60", "240"],
              supported_resolutions: Object.keys(resolutions),
              volume_precision: 8,
              data_status: "streaming",
              format: "price",
            });
          } catch (e) {
            fail(e.message);
            this.onError(e.message);
          }
        },
        getBars: async (info, resolution, period, cb, fail) => {
          try {
            if (!resolutions[resolution]) throw new Error("Unsupported chart timeframe");
            const data = await this.history(info, resolution, Math.floor(period.to),
              Math.min(4800, Math.max(1, period.countBack || 300)));
            const bars = (data.candles || []).map(toBar);
            const key = this.query(info, resolution);
            this.rememberCandles(key,data.candles || []);
            if(period.firstDataRequest && bars.length) this.priceData.set(key,data);
            if (period.firstDataRequest && bars.length) {
              this.barTimes.set(key, bars.slice(-20).map((b) => b.time / 1000));
              if (key === this.query({ ticker: ticker(this.venue, this.symbol) }, this.resolution)) {
                this.onAvailability(true);
                this.onPrice(data.candles.at(-1), data);
                setTimeout(() => this.redrawPlan(), 0);
              }
            }
            if (period.firstDataRequest && bars.length)
              this.lastTimes.set(
                this.query(info, resolution),
                bars.at(-1).time,
              );
            cb(
              bars.map((b) => ({ ...b })),
              { noData: bars.length === 0 },
            );
          } catch (e) {
            fail(e.message);
            const key = this.query(info, resolution);
            if (!this.barTimes.has(key)) this.onAvailability(false, "Could not load candles for this market:" + e.message);
            this.onStatus("Market data read failed", true);
            this.onError(e.message);
          }
        },
        subscribeBars: (info, resolution, cb, uid, reset) => {
          const key = this.query(info, resolution);
          let connection = this.connections.get(key);
          if (!connection) {
            connection = {
              key,
              venue: instrument(info.ticker || info.name).venue,
              symbol: instrument(info.ticker || info.name).symbol,
              resolution,
              listeners: new Map(),
              source: null,
            };
            this.connections.set(key, connection);
          }
          connection.listeners.set(uid, {
            cb, reset,
            last: this.lastTimes.get(key) || 0,
          });
          if (!this.paused && !connection.source) this.connect(connection);
        },
        unsubscribeBars: (uid) => {
          for (const [key, c] of this.connections) {
            c.listeners.delete(uid);
            if (!c.listeners.size) {
            c.source?.close();
            c.source = null;
              this.connections.delete(key);
            }
          }
        },
      };
    }
    connect(connection) {
      if (this.disposed || this.paused) return;
      const source = new EventSource("/api/planning/feed?" + connection.key);
      connection.source = source;
      let pending = Promise.resolve();
      source.onmessage = (event) => {
        pending = pending.then(() => this.receive(connection, source, event));
        return pending;
      };
      source.onerror = () => {
        if (!this.paused && connection.source === source && connection.venue === this.venue && connection.symbol === this.symbol && connection.resolution === this.resolution)
          this.onStatus("Market feed disconnected · reconnecting", true);
      };
    }
    async receive(connection, source, event) {
        if (connection.source !== source || this.paused) return;
        try {
          const message = JSON.parse(event.data);
          if (message.type === "feed_error") {
            this.onStatus("Market feed unavailable · reconnecting", true);
            if (!this.barTimes.has(connection.key)) this.onAvailability(false, "Could not load candles for this market:" + (message.data?.message || "Market feed connection failed"));
            return;
          }
          if (message.type !== "candles") return;
          const data = message.data,
            rows = data.candles || [];
          this.rememberCandles(connection.key,rows);
          const step = (connection.resolution === "1D" ? 86400 : Number(connection.resolution)*60)*1000;
          let incomplete = false;
          for (const listener of connection.listeners.values()) {
            let batch = rows.map(toBar).sort((a,b) => a.time-b.time);
            const first = batch.find(b => b.time > listener.last);
            if (listener.last && first && first.time > listener.last + step) {
              this.onStatus("Backfilling missing candles…", true);
              const missing = await this.history({ticker:ticker(connection.venue,connection.symbol)},
                connection.resolution, first.time/1000,
                Math.min(4800, Math.ceil((first.time-listener.last)/step)));
              batch = [...missing.candles.map(toBar), ...batch].sort((a,b) => a.time-b.time);
            }
            // A late response from a closed/replaced stream must never mutate the chart.
            if (connection.source !== source || this.paused || this.disposed || ![...connection.listeners.values()].includes(listener)) return;
            const forward = batch.filter(b => b.time >= listener.last);
            let cursor = listener.last;
            const gap = forward.some(b => {
              if (cursor && b.time > cursor + step) return true;
              cursor = b.time; return false;
            });
            if (gap) {
              this.onStatus("Candle gap detected · reload the chart", true);
              if (!listener.gap) { listener.gap = true; listener.reset?.(); }
              incomplete = true;
              continue; // Never fabricate OHLC or append across a missing interval.
            }
            listener.gap = false;
            for (const bar of forward) {
              listener.cb({ ...bar });
              listener.last = bar.time;
            }
          }
          if (incomplete) return;
          if (
            connection.venue === this.venue &&
            connection.symbol === this.symbol &&
            connection.resolution === this.resolution
          ) {
            const previous = this.barTimes.get(connection.key) || [];
            if (!rows.length) {
              this.onStatus("No candles available for this market", true);
              if (!previous.length) this.onAvailability(false, "The exchange returned no candles for this market. Check the symbol and try again.");
              return;
            }
            if (previous.length && rows.at(-1).time < previous.at(-1)) return;
            this.lastReceived = Date.now();
            this.onAvailability(true);
            const next = [...new Set([...previous, ...rows.filter((r) => !previous.length || r.time >= previous.at(-1)).map((r) => r.time)])].sort((a, b) => a - b).slice(-20);
            this.barTimes.set(connection.key, next);
            this.onStatus(
              data.stale
                ? "Market data is stale"
                : data.mode === "demo"
                  ? "Demo data · not live prices"
                  : "Market feed active · updates every 3 seconds",
              data.stale,
            );
            if (rows.length) this.onPrice(rows.at(-1), data);
            this.priceData.set(connection.key,data);
          }
        } catch {
          this.onStatus("Invalid market data", true);
        }
    }
    pause(value) {
      this.paused = value;
      for (const c of this.connections.values()) {
        c.source?.close();
        c.source = null;
        if (!value) this.connect(c);
      }
      this.onStatus(value ? "Market feed paused" : "Reconnecting market feed…", value);
    }
    async setContext(venue, symbol, resolution = this.resolution) {
      instrument(ticker(venue, symbol));
      await this.whenReady();
      await this.saveLayout(false);
      this.plotVersion++;
      this.removeOverlays();
      this.clearAI();
      this.planKey = "";
      this.plan = null;
      this.comparisonPlans=[];
      this.venue = venue;
      this.symbol = symbol;
      this.resolution = resolution;
      this.lastReceived = 0;
      this.onAvailability(false, "Loading " + symbol + " candles…");
      this.onContext(this.context());
      this.onStatus("Connecting market feed…", false);
      await new Promise((resolve, reject) => {
        const timer = setTimeout(
          () => reject(new Error("Market chart timed out. Check the connection and try again.")),
          20000,
        );
        this.widget.setSymbol(ticker(venue, symbol), resolution, () => {
          clearTimeout(timer);
          resolve();
        });
      });
      try {
        const raw = localStorage.getItem(this.layoutKey());
        if (raw && raw.length < 2e6) await this.widget.load(JSON.parse(raw));
        this.restoreAutoScale();
      } catch {
        this.onError("Could not restore the saved layout.");
      }
    }
    async setResolution(r) {
      if (!resolutions[r]) return;
      await this.whenReady();
      if (this.resolution !== r) this.clearAI();
      this.resolution = r;
      this.onContext(this.context());
      this.changingResolution = true;
      const resolutionVersion = this.resolutionVersion = (this.resolutionVersion || 0) + 1;
      this.plotVersion++;
      this.removeOverlays();
      this.planKey = "";
      try {
        await this.chart.setResolution(r);
        if(this.chart.dataReady) await new Promise(resolve=>this.chart.dataReady(resolve));
      } finally {
        if (resolutionVersion === this.resolutionVersion) {
          this.changingResolution = false;
          this.redrawPlan();
          this.restorePrice();
        }
      }
    }
    removeOverlays() {
      if (this.chart)
        for (const id of this.overlays) {
          try {
            this.chart.removeEntity(id);
          } catch {}
        }
      this.overlays = [];
    }
    restorePrice() {
      const data=this.priceData.get(this.query({ticker:ticker(this.venue,this.symbol)},this.resolution));
      if(data?.candles?.length) this.onPrice(data.candles.at(-1),data);
    }
    async plot(plan, phase = "draft", comparisons = []) {
      const comparisonPlans=comparisons.filter(p=>p.snapshot.kind==="perpetual" && p.snapshot.venue===this.venue && p.snapshot.symbol===this.symbol).slice(0,4);
      const key = (plan ? plan.hash + phase : "")+comparisonPlans.map(p=>p.hash).join(":");
      this.comparisonPlans=comparisonPlans;
      if (key === this.planKey && (plan ? this.plan?.hash === plan.hash : !this.plan)) return;
      this.planKey = key;
      this.plan = plan;
      this.planPhase = phase;
      const version = ++this.plotVersion;
      await this.ready;
      if (!this.chart || this.disposed) return;
      if (this.changingResolution) {this.planKey="";return;}
      // Native interval switches rebuild the series asynchronously. Cached bar
      // times alone do not mean the chart can accept price-coordinate shapes.
      if (this.chart.dataReady) await new Promise(resolve => this.chart.dataReady(resolve));
      if (version !== this.plotVersion) return;
      this.removeOverlays();
      if (
        !plan ||
        (plan.snapshot.kind && plan.snapshot.kind!=="perpetual") ||
        plan.snapshot.venue !== this.venue ||
        plan.snapshot.symbol !== this.symbol
      ) return;
      const times = this.barTimes.get(this.query({ ticker: ticker(this.venue, this.symbol) }, this.resolution)) || [];
      if (times.length < 2) {
        this.planKey = "";
        return;
      }
      // Anchor both edges on available candles. Future-only points can snap to
      // the same last candle and collapse a rectangle into an invisible line.
      const start = times[Math.max(0, times.length - 12)], end = times.at(-1);
      const common = {
        lock: true,
        disableSelection: true,
        disableSave: true,
        disableUndo: true,
        zOrder: "top",
      };
      const make = async (p, options) => {
        const id = await this.chart.createMultipointShape(p, options);
        if (version === this.plotVersion) this.overlays.push(id);
        else
          try {
            this.chart.removeEntity(id);
          } catch {}
      };
      try {
        if(comparisonPlans.length>=2) {
          const cached=this.candleData.get(this.query({ticker:ticker(this.venue,this.symbol)},this.resolution)) || [];
          let visible=cached;try{const range=this.chart.getVisibleRange?.();if(range?.to>range?.from)visible=cached.filter(r=>r.time>=range.from && r.time<=range.to);}catch{}
          const comparisonTimes=visible.length>=6?visible.map(r=>r.time):times;
          await this.paintComparisons(comparisonPlans,plan.id,comparisonTimes,common,make,version);
          if(version===this.plotVersion && this.aiCandidates.length)await this.draw(this.aiCandidates.slice(),"ai");
          return;
        }
        const entries=plan.idea.execution?.kind==="ladder" ? plan.orders.map(o=>["Limit order "+o.index+" · "+Number(o.quantity).toLocaleString("en-US",{maximumFractionDigits:8}),o.price,"#9b6a14"]) : [[plan.idea.execution?.kind==="twap"?"TWAP · "+plan.orders.length+" slices · T+0s to T+"+Math.max(...plan.orders.map(o=>o.offset_seconds))+"s":plan.idea.execution?.kind==="market"?"Est. market entry":"Entry",plan.idea.entry,"#9b6a14"]];
        for (const [label, price, color] of [
          ...entries,
          ["Stop loss", plan.idea.stop, "#D6433A"],
          ["Take profit", plan.idea.target, "#2E8B57"],
        ]) {
          const id = await this.chart.createShape(
            { time: start, price: Number(price) },
            {
              ...common,
              shape: "horizontal_line",
              text:
                label + (label==="Stop loss"?" −"+Number(plan.risk).toFixed(2):label==="Take profit"?" +"+Number(plan.reward).toFixed(2):"") +
                " · " +
                Number(price).toLocaleString(),
              overrides: {
                linecolor: color,
                linewidth: 2,
                showLabel: true,
                showPrice: true,
                textcolor: color,
                fontsize: 11,
                horzLabelsAlign: "right",
                vertLabelsAlign: "top",
              },
            },
          );
          if (version === this.plotVersion) this.overlays.push(id);
          else
            try {
              this.chart.removeEntity(id);
            } catch {}
        }
        for (const [p, color] of [
          [plan.idea.stop, "#D6433A"],
          [plan.idea.target, "#2E8B57"],
        ])
          await make(
            [
              { time: start, price: Number(plan.idea.entry) },
              { time: end, price: Number(p) },
            ],
            {
              ...common,
              shape: "rectangle",
              zOrder: "bottom",
              overrides: {
                color,
                backgroundColor: color,
                fillBackground: true,
                transparency: 86,
                linewidth: 1,
              },
            },
          );
        // Automatic painting must preserve the user's time/price viewport.
        if (version === this.plotVersion && this.aiCandidates.length) await this.draw(this.aiCandidates.slice(),"ai");
      } catch (e) {
        if (version === this.plotVersion) this.planKey = "";
        this.onError("Could not draw the plan overlay:" + e.message);
      }
    }
    async paintComparisons(plans,selected,times,common,make,version) {
      const colors=["#9b6a14","#3575a8","#8050a1","#237a69"],letters=["A","B","C","D"],span=Math.min(times.length-1,Math.max(2,Math.floor((times.length-1)*0.72))),base=times.length-1-span,lane=Math.max(2,Math.floor(span/plans.length));
      for(let i=0;i<plans.length;i++) {
        if(version!==this.plotVersion)return;
        const p=plans[i],orders=p.orders?.length?p.orders:[{price:p.idea.entry,quantity:p.quantity}],total=orders.reduce((n,o)=>n+Number(o.quantity),0),entry=total?orders.reduce((n,o)=>n+Number(o.price)*Number(o.quantity),0)/total:Number(p.idea.entry);
        const start=times[Math.min(times.length-2,base+i*lane)],end=times[Math.min(times.length-1,base+(i+1)*lane-1)],active=p.id===selected,color=colors[i],tag=letters[i];
        for(const [price,c]of [[p.idea.stop,"#D6433A"],[p.idea.target,"#2E8B57"]])await make([{time:start,price:entry},{time:end,price:Number(price)}],{...common,shape:"rectangle",zOrder:"bottom",overrides:{color:c,backgroundColor:c,fillBackground:true,transparency:active?84:93,linewidth:active?1:0}});
        const levels=active?orders.map(o=>[Number(o.price),color]):[[entry,color]];
        levels.push([Number(p.idea.stop),"#D6433A"],[Number(p.idea.target),"#2E8B57"]);
        for(const [price,c]of levels)await make([{time:start,price},{time:end,price}],{...common,shape:"trend_line",text:"",overrides:{linecolor:c,linewidth:active?2:1,linestyle:active?0:2,extendLeft:false,extendRight:false,showLabel:false,showPrice:false,showDistance:false,showAngle:false}});
        const fmt=x=>window.PlanCompare?.format(x) || Number(x).toLocaleString("en-US",{maximumFractionDigits:4});
        const labels=[[Number(p.idea.target),tag+" · "+(p.idea.side==="long"?"Long":"Short")+" Target "+fmt(p.idea.target),"#2E8B57"],[entry,(orders.length>1 && p.idea.execution?.kind==="ladder"?"Average entry ≈ ":p.idea.execution?.kind==="market"?"Est. market entry ":"Entry ")+fmt(entry),color],[Number(p.idea.stop),"Stop loss "+fmt(p.idea.stop),"#D6433A"]];
        for(const [price,text,c]of labels){
          const label=await this.chart.createShape({time:text.startsWith("Stop loss")?times[Math.min(times.length-1,base+i*lane+Math.floor(lane*0.45))]:start,price},{...common,shape:"text",text,overrides:{color:c,textcolor:c,fontsize:text.startsWith("Stop loss")?12:14,bold:active}});
          if(version===this.plotVersion)this.overlays.push(label);else try{this.chart.removeEntity(label);}catch{}
        }
      }
    }
    async focusPlan() {
      await this.whenReady();if(!this.plan) return;
      if(this.chart.dataReady) await new Promise(resolve=>this.chart.dataReady(resolve));
      const last=this.lastTimes.get(this.query({ticker:ticker(this.venue,this.symbol)},this.resolution));
      const seconds={"1":60,"5":300,"15":900,"60":3600,"240":14400,"1D":86400}[this.resolution];
      // Future timestamps have no native time-scale index. Let the chart add
      // right-side space using its documented margin option instead.
      if(last && seconds && this.chart.setVisibleRange) await this.chart.setVisibleRange({from:last/1000-64*seconds,to:last/1000},{percentRightMargin:12});
      this.fitPlan();
    }
    fitPlan() {
      if (!this.chart || !this.plan || this.disposed || this.changingResolution) return;
      if(this.chart.dataReady && !this.chart.dataReady()) return;
      const key=this.query({ticker:ticker(this.venue,this.symbol)},this.resolution);
      const context=this.context(), cached=this.candleData.get(key) || [];
      const visible=cached.filter(r=>(!context.visible_from || r.time>=context.visible_from) && (!context.visible_to || r.time<=context.visible_to));
      if(!visible.length) {this.restoreAutoScale();return;}
      const values = [
          ...visible.flatMap(r=>[r.high,r.low]),
          this.plan.idea.entry,
          this.plan.idea.stop,
          this.plan.idea.target,
          ...(this.plan.orders || []).map(o=>o.price),
          ...(this.comparisonPlans || []).flatMap(p=>[p.idea.entry,p.idea.stop,p.idea.target,...(p.orders || []).map(o=>o.price)]),
        ].map(Number).filter(n=>Number.isFinite(n) && n>0),
        lo = Math.min(...values),
        hi = Math.max(...values),
        pad = (hi - lo) * 0.15 || hi * 0.01;
      const scale = this.chart.getPanes()[0]?.getMainSourcePriceScale();
      if (scale) {
        scale.setAutoScale(false);
        scale.setVisiblePriceRange({ from: lo - pad, to: hi + pad });
      }
    }
    annotationKind(d) {
      if (/support|\bS[1-9]\b/i.test(d.label)) return "support";
      if (/resistance|\bR[1-9]\b/i.test(d.label)) return "resistance";
      return "other";
    }
    selectAI(drawings) {
      if(this.comparisonPlans?.length>=2)return [];
      const levels=this.plan?.snapshot.venue===this.venue && this.plan?.snapshot.symbol===this.symbol ?
        [this.plan.idea.entry,this.plan.idea.stop,this.plan.idea.target,...(this.plan.orders || []).map(o=>o.price)].map(Number):[];
      const picked=[];
      for(const d of drawings) {
        const price=Number(d.points[0]?.price);
        if(d.kind==="horizontal_line" && (levels.some(p=>Math.abs(p-price)<=Math.max(p,price)*0.00015) || picked.some(other=>other.kind==="horizontal_line" && Math.abs(Number(other.points[0]?.price)-price)<=price*0.00015))) continue;
        picked.push(d);
      }
      drawings=picked;
      if (this.expandedAI || drawings.length<=4) return drawings.slice();
      // Preserve representation on both sides, independent of model ordering.
      const support=drawings.filter(d=>this.annotationKind(d)==="support");
      const resistance=drawings.filter(d=>this.annotationKind(d)==="resistance");
      const balanced=support.slice(0,2).concat(resistance.slice(0,2));
      for(const d of drawings) if(balanced.length<4 && !balanced.includes(d)) balanced.push(d);
      return balanced;
    }
    annotationState() {
      return {total:this.aiCandidates.length,visible:this.aiOverlays.length,expanded:this.expandedAI,shown:this.showAI && !(this.comparisonPlans?.length>=2),comparing:this.comparisonPlans?.length>=2,items:this.showAI?this.aiDrawings.slice():[]};
    }
    clearAI(reset=true) {
      this.aiRequest++;
      this.aiVersion++;
      if(this.chart) for(const id of this.aiOverlays) {try{this.chart.removeEntity(id);}catch{}}
      this.aiOverlays=[];this.aiDrawings=[];
      if(reset) {this.aiCandidates=[];this.expandedAI=false;}
      this.onAnnotations(this.annotationState());
    }
    async draw(drawings,source="manual",interval=this.context().interval) {
      const scene=this.venue+":"+this.symbol+":"+this.resolution;
      const request=source==="ai"?++this.aiRequest:0;
      await this.whenReady();
      if(this.chart.dataReady) await new Promise(resolve=>this.chart.dataReady(resolve));
      if(this.disposed || this.changingResolution || scene!==this.venue+":"+this.symbol+":"+this.resolution || source==="ai" && request!==this.aiRequest) return;
      if(source==="ai") {
        if(interval!==this.context().interval) return;
        this.clearAI(false);this.aiCandidates=drawings.slice(0,10);this.aiDrawings=this.selectAI(this.aiCandidates);
        this.onAnnotations(this.annotationState());
        if(!this.showAI) return;
        drawings=this.aiDrawings;
      }
      const version=this.aiVersion;
      for (const d of drawings) {
        if(this.disposed || this.changingResolution || scene!==this.venue+":"+this.symbol+":"+this.resolution || source==="ai" && version!==this.aiVersion) return;
        const anchor=this.barTimes.get(this.query({ticker:ticker(this.venue,this.symbol)},this.resolution))?.at(-1);
        const points = d.points.map((p) => ({
          time: source==="ai" && d.kind==="horizontal_line" && anchor ? anchor : p.time,
          price: Number(p.price),
        }));
        const color=source==="ai" ? ({support:"#2E8B57",resistance:"#D6433A",other:"#3575a8"}[this.annotationKind(d)]) : "#3575a8";
        const options = {
          shape: d.kind,
          text: source==="ai"?"":d.label,
          ...(source==="ai"?{lock:true,disableSave:true,disableSelection:true,disableUndo:true}:{}),
          overrides: {
            linecolor: color,
            color,
            backgroundColor: color,
            transparency: 92,
            linewidth: 1,
            showLabel: source!=="ai",
            showPrice: source!=="ai",
            linestyle: source==="ai"?2:0,
            textcolor: color,
          },
        };
        const id=d.kind==="horizontal_line" ? await this.chart.createShape(points[0],options) : await this.chart.createMultipointShape(points,options);
        if(source==="ai") {if(version===this.aiVersion) this.aiOverlays.push(id);else try{this.chart.removeEntity(id);}catch{}}
      }
      if(source==="ai") this.onAnnotations(this.annotationState());
      await this.saveLayout(false);
    }
    async toggleAI() {
      this.showAI=!this.showAI;
      if(!this.showAI) this.clearAI(false); else await this.draw(this.aiCandidates.slice(),"ai");
    }
    async toggleAllAI() {
      this.expandedAI=!this.expandedAI;
      await this.draw(this.aiCandidates.slice(),"ai");
    }
    drawings() {
      if (!this.chart) return [];
      const drawings = [];
      for (const info of this.chart.getAllShapes()) {
        if (this.overlays.includes(info.id) || this.aiOverlays.includes(info.id)) continue;
        const name = String(info.name).toLowerCase();
        const kind = /horizontal|horz/.test(name)
          ? "horizontal_line"
          : /trend/.test(name)
            ? "trend_line"
            : /rectangle/.test(name)
              ? "rectangle"
              : null;
        if (!kind) continue;
        try {
          const shape = this.chart.getShapeById(info.id),
            points = shape
              .getPoints()
              .slice(0, kind === "horizontal_line" ? 1 : 2)
              .map((p) => ({
                time: Math.floor(p.time),
                price: Number(p.price).toFixed(8),
              }));
          if (points.length === (kind === "horizontal_line" ? 1 : 2))
            drawings.push({
              kind,
              label: String(shape.getProperties().text || info.name).slice(
                0,
                80,
              ),
              points,
            });
        } catch {}
        if (drawings.length === 20) break;
      }
      return drawings;
    }
    async saveLayout(show = true) {
      if (!this.chart || this.disposed || this.changingResolution) return;
      if(this.chart.dataReady && !this.chart.dataReady()) return;
      await new Promise((resolve) =>
        this.widget.save((data) => {
          try {
            const raw = JSON.stringify(data);
            if (raw.length < 2e6) localStorage.setItem(this.layoutKey(), raw);
            else throw new Error("Chart layout exceeds the size limit");
            if (show) this.onStatus("Chart layout saved in this browser", false);
          } catch {
            this.onError("Browser storage is unavailable; chart layout was not saved.");
          }
          resolve();
        }),
      );
    }
    async screenshot() {
      await this.whenReady();
      const canvas = await this.widget.takeClientScreenshot();
      canvas.toBlob((blob) => {
        if (!blob) return;
        const url = URL.createObjectURL(blob),
          a = document.createElement("a");
        a.href = url;
        a.download = "vergex-" + this.symbol + ".png";
        a.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      });
    }
    dispose() {
      this.disposed = true;
      clearInterval(this.ageTimer);
      clearTimeout(this.readyTimer);
      clearTimeout(this.comparisonViewportTimer);
      this.readyResolve();
      for (const c of this.connections.values()) c.source?.close();
      this.connections.clear();
      this.widget?.remove();
    }
  }
  window.WorkspaceChart = WorkspaceChart;
})();
