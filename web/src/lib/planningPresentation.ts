export function validationSummary(reason: string): string {
  const ratio = reason.match(
    /net reward\/risk ([\d.]+) is below minimum ([\d.]+)/i
  )
  if (ratio) {
    const value = Number(ratio[1]),
      minimum = Number(ratio[2])
    if (Number.isFinite(value) && Number.isFinite(minimum)) {
      return value.toFixed(2) === minimum.toFixed(2)
        ? `Net R:R is just below ${minimum.toFixed(2)} : 1 after costs.`
        : `Net R:R ${value.toFixed(2)} : 1 is below the required ${minimum.toFixed(2)} : 1 after costs.`
    }
  }
  return reason
}

// Polling run timestamps must not recreate immutable chart plans and redraw
// all annotations. Keep references until the actual plan or state changes.
export function preservePlanningPlans(
  previous: import('./planning').PlanningState,
  incoming: import('./planning').PlanningState
): import('./planning').PlanningState {
  const records = Object.fromEntries(
    Object.entries(incoming.records).map(([id, record]) => {
      const old = previous.records[id]
      if (!old) return [id, record]
      const plan =
        JSON.stringify(old.plan) === JSON.stringify(record.plan)
          ? old.plan
          : record.plan
      return [
        id,
        old.state === record.state && plan === old.plan
          ? old
          : { ...record, plan },
      ]
    })
  )
  const sameRecords =
    Object.keys(records).length === Object.keys(previous.records).length &&
    Object.keys(records).every((id) => records[id] === previous.records[id])
  const runs = incoming.runs.map((run) => {
    const old = previous.runs.find((item) => item.id === run.id)
    return old && JSON.stringify(old.plan) === JSON.stringify(run.plan)
      ? { ...run, plan: old.plan }
      : run
  })
  return {
    ...incoming,
    records: sameRecords ? previous.records : records,
    runs,
  }
}
