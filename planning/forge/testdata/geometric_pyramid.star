def plan(market, args):
    mid = float(market["mid"])
    gap = float(args["gap_bps"]) / 10000
    if gap <= 0 or gap > 0.01:
        fail("gap_bps must be 1-100")
    tick = market["tick"]
    levels = []
    weights = allocate_weights([1, 2, 4])
    for i in range(3):
        entry = price(mul(mid, 1 - mul(i, gap)), tick)
        append(levels, {"price": entry, "weight": weights[i]})
    return {"ideas": [{"name": "Geometric weighted ladder", "side": "long", "entry": price(mid, tick), "stop": price(mul(mid, 0.99), tick), "target": price(mul(mid, 1.04), tick), "rationale": "Conditional pullback ladder with quantity weights 1:2:4; no fill or profit guarantee", "execution": {"kind": "ladder", "levels": levels}}]}
