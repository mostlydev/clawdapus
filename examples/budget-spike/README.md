# Budget Spike

This example demonstrates proxy-enforced compute budgets with a local fake
OpenAI-compatible provider.

```bash
claw pull --no-runners
claw build
claw up -d
```

The `analyst` agent has `x-claw.budget` inherited from `budget-defaults`. The
fake provider reports enough token usage for the first successful turn to exceed
the tiny USD cap, so the next turn is rejected by cllama with HTTP 429 and a
`budget_exceeded` intervention. A Master Claw or operator can raise the live cap
through `fleet.budget.set`; cllama reads the override from the governance mount
on the next request.
