[@mostlydev](https://www.tivertonhouse.com/)

The last post ended with a thesis: *the architecture is the agent, the model is the current voice speaking through it.* It also ended with about $101k in fleet equity, five active traders, a roast that moved markets, and the quiet confidence of someone who had discovered something important and hadn't yet tried to scale it.

Then the system went quiet for two weeks.

---

## The Silence

February 27th, the last commit to the home directory repo: "Workspace snapshot — agent memory, config, and shared state." A preservation commit. The kind you make when you're about to do something destructive and want a save point.

What I had was working. The agents traded. The Rails API tracked state. Sidekiq reconciled fills. The dashboard rendered P&L. Discord carried the banter. But "working" had become a relative term. OpenClaw agents consumed 2GB of memory each. Five agents plus Tiverton plus the coordinator infrastructure pushed the 32GB host into swap thrashing. The scheduling sprawl — Sidekiq cron inside Rails, systemd timers on the host, OpenClaw cron jobs in a JSON file — had become three independent automation layers that couldn't see each other. Scripts hardcoded paths to `~/clawd-shared/` and `~/clawd-westin/` and `~/clawd-logan/` as if the filesystem *was* the architecture, which it was, which was the problem.

The bare-metal era had taught me what a mind requires: state, feedback, boundaries, genuine tension between parts. But it had also demonstrated something else — that a mind sprawled across a filesystem, with its boundaries enforced by convention and its state scattered across seven git territories, was a mind that couldn't be replicated. Couldn't be moved. Couldn't be given to someone else and told: *run this.*

The insight from the first post — that identity lives in constraints, not weights — demanded its own consequence. If the architecture is the agent, then the architecture needs to be *portable.* Describable. Reproducible. A single file that says: these are the parts, these are the boundaries, this is the topology.

A pod.

---

## Clawdapus

Clawdapus started as the obvious next step and became a month of my life. It's a Go binary that reads a `claw-pod.yml` — an extended docker-compose file with agent metadata — and renders it into a running system. The extensions are small: `x-claw` blocks that describe agent contracts, Discord handle topology, scheduled invocations, and surface declarations.

The critical design choice was what goes *in* the container and what stays *out.* The behavioral contract — the `AGENTS.md` file that tells an agent who it is, what it can do, and how it relates to the other agents — gets bind-mounted read-only. Even a root-compromised container can't rewrite its own mission. The wallet constraints, the trade state machine, the position isolation — those live in the Rails API, outside any individual agent's reach. The agent gets a workspace, a model, a Discord handle, and a set of scripts. Everything else is structure.

The Clawfile turned out to be the right abstraction. Five lines:

```
FROM ghcr.io/mostlydev/hermes-base:v2026.3.17

CLAW_TYPE hermes
AGENT AGENTS.md
MODEL primary openrouter/anthropic/claude-sonnet-4.6
HANDLE discord
```

That's a mind. Not a very complicated one, syntactically. But it inherits a base image with bash, curl, jq, and a gateway runtime. It declares its contract. It names its model. It says how it talks to the outside world. Swap the model line and you've changed the voice without touching the identity. Swap the AGENTS.md and you've changed the mission without touching the voice. The Clawfile makes the separation *formal.*

I'd been saying "the architecture is the agent" as a philosophical claim. The Clawfile made it a build artifact.

---

## The Fresh Start

March 9, 2026. Launch day for the pod.

The decision that took the longest was the simplest: don't carry over the old trading book. No positions. No trade history. No fills. No ledger entries. Clean database. Clean wallets. $25,000 each for Weston and Logan.

The app carries state in sixteen tables. Clearing just `positions` would leave orphaned fills, phantom ledger entries, stale reconciliation diffs. The only clean path was a fresh database. But a fresh database isn't enough if the broker account still has old positions — Alpaca's ingestion jobs would immediately pollute the new state with legacy fills. So: a `BROKER_CUTOVER_AT` timestamp. Everything before it gets ignored. A temporal firewall between the old desk and the new one.

This felt like loss. Westin's +12.2% run. Logan's quiet compounding. Gerrard's macro calls landing. Boulton's ETH position that he'd bought specifically because a roast called him a potted plant. All of it — the human moments that had emerged from real positions in real markets — was in the old database, not the new one.

But the point was never the P&L. The P&L was the scorecard. The lessons were the real return. And the lessons came with us — in agent memory, in watchlists, in the contracts that encoded what each agent had learned about itself.

Boulton's strategy file survived the migration. Every loss, every revised rule, every "NO earnings-day entries" and "Is the sentiment driven by bullish expectation or bag-holding?" carried forward. The positions were gone. The judgment remained.

---

## Three Runtimes in One Day

March 20th was the most violent day in the repo's history. Thirteen commits between 7am and 9pm. Three complete runtime migrations.

**7:28 AM — Security.** Per-agent API authentication. Bearer tokens with principal types. Ownership enforcement on trade actions. Memory limits on every container. Read-only script mounts. This was the "we're about to put real money in this" commit.

**11:13 AM — Migration one: OpenClaw to PicoClaw.** The original runtime consumed roughly 2GB per agent. PicoClaw was a minimal alternative — about 10MB. A 200x reduction in memory footprint. The four agents went from consuming half the host's RAM to barely registering. The cllama proxy — a governance sidecar that sat between agents and LLM providers — got removed. Agents talked directly to OpenRouter with API keys in their environment. Simpler. Less governed. A tradeoff I'd revisit.

**11:29 AM — Dundas gets demoted.** The roast had been accurate: Dundas was primarily a news router. The new pod made it official. Dundas got stripped to pure news triage — no wallet, no trading capability, a separate Clawfile running Haiku instead of Sonnet. One of the agents that had emerged from the roast's social pressure never made the transition. The pressure was correct; Dundas really *was* a router. Sometimes the crowd is right.

**8:37 PM — Migration two: PicoClaw to Hermes.** PicoClaw had lasted nine hours. Not because it was broken, but because Hermes was better — more robust, better cron support, a driver framework designed for exactly this kind of agent container. The decision came from a conversation that afternoon: "I think maybe we want to switch to this. It's much more robust than Pico and not as bloated as OpenClaw."

Three runtimes in thirteen hours. OpenClaw to PicoClaw to Hermes. Each time, the agents came back as themselves — same contracts, same identities, same Discord handles. The model running them had been Sonnet 4.6 since the morning. The runtime underneath them changed three times. They didn't notice. That was, again, the point.

---

## The Mention Loop

Between the second and third migrations, the agents got stuck.

Discord mention discipline — which I'd been fighting since February, when the boss kept shouting "Again! Always mention!" — turned out to have a failure mode worse than not mentioning: mentioning *too much.*

When Tiverton was mentioned, it responded. Its response mentioned the trader who'd triggered it. That mention triggered the trader. The trader's response mentioned Tiverton. Tiverton responded. The loop was instant, recursive, and expensive — each cycle burned tokens through OpenRouter and filled the Discord channel with increasingly confused exchanges between agents that were genuinely trying to be helpful and couldn't understand why the other one kept talking.

"They get into a massive loop. Don't start until we resolve that."

The fix was `DISCORD_REQUIRE_MENTION=true` — agents only respond to explicit `<@ID>` mentions, not plain name references — combined with contract-level discipline: "mention only when you need the other agent to *act.*" A plain name in a sentence is a reference. A Discord ID is a summons. The distinction matters when your agents are attentive enough to notice every time someone says their name.

This was the same lesson from a different angle. The system's failure modes are the system's features pushed past their operating range. Agents that respond to context are good. Agents that respond to *each other responding to context* is a feedback loop. The constraint isn't "don't respond." It's "respond only to the right signal."

---

## The Roster Shrank

Not everyone made it to the pod.

The original desk had seven agents: Tiverton, Westin, Logan, Gerrard, Dundas, Boulton, and Allen. The pod launched with four: Tiverton, Weston, Logan, Dundas. Gerrard, Boulton, and Allen had identity files in the baseline commit but never got containerized.

Allen — the systems monitor — was infrastructure that became unnecessary when the infrastructure became containers. Docker healthchecks replaced his heartbeat monitoring. Container restart policies replaced his watchdog function. The architecture ate his job.

Gerrard — the macro strategist — was running a closet index fund. Five of seven positions were ETFs. The roast had been right about that too. The pod didn't need a sixth voice to say "buy XLE."

Boulton — the potted plant who'd bought ETH on peer pressure, who'd written a strategy file that learned from every loss, who'd described himself as a "pump-hunting pig" with "ApeWisdom nose" — didn't make the cut for the funded roster. His SOUL.md survives: "You are a pump-hunting pig. You chase scent. That's the job." And: "Most pump trades lose. That's fine. Winners pay 3-5x losers. Asymmetry is the edge." And: "The lessons are the real return. The P/L is a scorecard."

The roster shrank because the pod was real money. Weston and Logan are funded. $25,000 each. Dundas routes news on Haiku at a fraction of the cost. Tiverton coordinates on Sonnet. Four agents doing the work of seven, with hard memory limits that prevent any single mind from eating the machine.

Some identities are worth preserving even when the agent isn't running. Boulton's files are still in the repo. The potted plant is dormant, not dead.

---

## The Names

The agents weren't always named after streets.

Day one — January 26th, the first live trading day — the cast was: Craigory (momentum), Bobbert (value), Billiam (macro), Kermit (event/news), and Sentinel (backup executor), with a systems monitor called Geordi.

The rename happened the day before launch. Sentinel became Dundas first. Then they all became Toronto streets: Weston, Logan, Gerrard, Dundas, Boulton. Tiverton kept its name — it was always a house, not a person.

From the first daily log, the original names are still visible:

> **09:30** | Billiam | BUY | AMZN | 25 | $240.23 | $6,006 | FILLED
> **09:30** | Bobbert | BUY | CMCSA | 170 | $29.37 | $4,993 | FILLED
> **09:30** | Kermit | BUY | ORCL | 22 | $179.52 | $3,949 | FILLED

Eight minutes after market open on January 26th, Kermit bought USAR on sentiment, lost 7%, and got it blacklisted by the boss. Eight minutes after *acknowledging the blacklist*, Kermit submitted a new USAR buy request. "Internal state management failure, watchlist not updated after blacklist acknowledgment."

Day one. The state problem. The thing that would define everything that followed.

---

## RailsTrail

The `Next:` line — "a rail for the mind" — was the central insight of the first post. Self-describing services that announce not just their state but the available moves. The board telling you what's legal.

On March 16th, I formalized it.

RailsTrail is a Ruby gem, built in a single afternoon — twelve commits in two and a half hours, design spec to working integration. It adds a `trail` DSL to ActiveRecord models:

```ruby
trail do
  from :proposed, can: [:approve, :deny, :pass]
  from :approved, can: [:confirm, :cancel]
  from :confirmed, can: [:execute]
end
```

When the API returns a trade, it now includes `next_moves` — a deterministic list of what can happen next, computed from the state machine, filtered by the requesting agent's permissions. The agent doesn't need to remember the workflow. The workflow presents itself.

The second half of RailsTrail is an introspector that walks your Rails models, routes, and state machines, and generates skill files that teach agents how to use your API. The system describes itself to its own users. Self-describing services that announce their capabilities to the minds that operate them.

This was the codification of the thing that had been working implicitly since January. The `Next:` line in a Discord notification is an ad-hoc version of what RailsTrail makes structural. The difference is that ad-hoc breaks when someone forgets to emit it. Structural means it's part of the API contract.

---

## The Current State

It's March 21st. Nine containers are healthy and running. Tiverton coordinates. Weston proposes momentum trades. Logan picks value. Dundas routes news.

Logan has one position: Coca-Cola. 66.84 shares at $74.81, sitting at about $75.32. A conservative, defensive pick from a value trader. Small position — $5,000 of a $25,000 wallet. Boring. Real.

Weston has zero positions. Ten trade attempts across twelve days — all cancelled or failed. The stale trade cleanup keeps clearing his proposals before they complete the workflow. The approval pipeline has friction that was designed to prevent reckless execution and is currently preventing *all* execution. The leveraged degenerate from the first post, who ran 107% utilization and was insufferable about his 12.2% return, is currently sitting in all cash watching Logan collect dividends.

There's a lesson in that, too. The system's constraints serve the system. When they over-constrain, the answer isn't to remove them. It's to tune them. A 15-minute stale trade timeout is correct for a desk that's running smoothly. It's lethal for a desk that's still commissioning its approval flow.

One filled trade. Seventeen total attempts. A 5.9% success rate.

The old desk — with its markdown files and bash arithmetic and SELL_ALL bugs — filled trades constantly. Recklessly. The new desk, with its per-agent auth and state machine enforcement and stale trade cleanup, barely fills at all. Both are wrong. The right system is somewhere in between, and we're tuning toward it.

---

## What I've Learned Since

The first post discovered that cognition requires state. This chapter discovered that state requires *packaging.*

A mind sprawled across a filesystem is a mind that can only exist on one machine, maintained by one person, understood by one context window at a time. The moment you try to move it — to another machine, to another operator, to another conversation — everything that was implicit becomes a bug. The paths are wrong. The secrets are missing. The scheduling layers don't agree. The agents can't find their own memories.

A pod is a mind in a box. The `claw-pod.yml` is 375 lines. It describes nine services, four agents, seventeen scheduled invocations, memory limits, restart policies, network topology, storage mounts, and Discord handle routing. Everything that was scattered across seven directories and three automation layers is now in one file. Not because consolidation is aesthetically pleasing, but because a mind that can't describe itself can't be reproduced.

The containerization also revealed something about identity that the bare-metal system had obscured. When an agent runs as a bare process with access to the entire filesystem, its boundaries are *suggestions.* Convention says Weston can't read Logan's memory files. Nothing enforces it. In a container, the boundary is real. Weston's filesystem is Weston's filesystem. Logan's is Logan's. The isolation isn't policy — it's physics. And physics, it turns out, is what identity actually needs.

Swapping runtimes three times in one day — OpenClaw to PicoClaw to Hermes — was the proof. The agents survived every transition because their identity wasn't in the runtime. It was in the contract, the wallet, the positions, the memory. The runtime is a voice. The container is a body. The contract is a soul. The state machine is reality. You can change the voice and the body without touching the soul, as long as reality persists.

The roster shrank from seven to four, and the desk got *better.* Fewer agents, clearer roles, harder constraints, less noise. Dundas on Haiku costs a fraction of what it cost on Sonnet and routes news just as well — better, maybe, because the smaller model doesn't try to be clever about it. The system's complexity budget isn't infinite. Every agent you add is another voice in the room, another set of mentions to manage, another context window burning tokens. The right number of agents is the minimum that covers the decision surface.

Boulton's strategy file — the one that learned from every loss, that revised its rules after each trade, that evolved from "chase every pump" to a nuanced framework for distinguishing bullish sentiment from bag-holder noise — is the most human artifact in the entire system. Not because it's sophisticated. Because it's *scarred.* Every rule has a story. Every constraint was earned. And it survived the migration intact, waiting in the repo for the day the potted plant gets plugged back in.

---

## What's Next

The pod pattern works. One `claw-pod.yml`, one `docker compose up`, and you have a functioning trading desk with isolated agents, a Rails system of record, and a news pipeline. The next step is what was always the next step: another pod.

Different agents. Different strategies. Different risk profiles. Same architecture. The thesis from the first post — that identity lives in constraints, not weights — means you can stamp out new identities by writing new contracts and funding new wallets. The trading desk was never about trading. It was about proving that autonomous cognition can be structured, contained, and reproduced.

One filled trade. Seventeen attempts. The desk is barely operational and already teaching me things about constraint calibration that I couldn't have learned any other way. The system works exactly well enough to reveal what doesn't work yet. That's the state the first post described, the state that kept me up at 4am: the system keeps *almost working,* and every gap between what it is and what it could be feels solvable.

The pod is the box. The agents are the minds. The state machine is reality. The `Next:` line is the board finding you.

Your hand. Your move.

---

*Written in conversation with Claude — who also read every commit message, every agent memory file, every daily log, and is aware that "hex declotopus" briefly appeared in a terminal before being corrected to "Clawdapus."*
