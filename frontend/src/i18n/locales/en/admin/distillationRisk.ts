export default {
  distillationRisk: {
    concurrency_saturation: 'Concurrency saturation ratio',
    request_rate_saturation: 'Rate saturation ratio',
    prompt_repeat_ratio: 'Exact repeat ratio',
    interarrival_cv: 'Request interval CV',
    output_input_ratio: 'Output/input ratio',

    actionReview: 'Start review',
stale: 'The last successful snapshot is shown. Refresh failed.',telemetryDegraded: 'Telemetry is degraded; automatic actions require complete evidence.',restrictionUntil: 'Manual restriction expires at',restrictionHint: 'Choose an explicit expiry within the next 24 hours.',
    title: 'Distillation behavior risk', description: 'Review patterns across all API keys and groups using recorded evidence. Behavior scores identify cases for review; they do not establish intent.',
    configuration: 'Configuration', mode: 'Operating mode', pendingReviews: 'Pending reviews', activeRestrictions: 'Active limits', lastProcessed: 'Last window processed',
    evidenceHint: 'Only complete UTC 30-minute windows, sealed after 10 minutes, with at least 200 workload units and 100,000 tokens can be scored. Missing signals appear as unavailable.',
    userID: 'User ID', allStates: 'All states', sections: 'Risk sections', review: 'Review', evidence: 'Evidence', score: 'Score', reviewStatus: 'Review status', reason: 'Action reason', windows: 'Completed windows', noWindows: 'No completed evidence windows.', units: 'units', unavailable: 'Unavailable',
    revokeExemption: 'Revoke exemption', createExemption: 'Create exemption', exemptionUntil: 'Exemption expires at', exemptionHint: 'Automatic actions only. Choose a future time within 30 days; manual limits remain in effect.',
    snapshotAt: 'Loaded at {time}', configHint: 'Observe mode records evidence without automatic penalties. Enforcement requires at least seven observation days. Turning enforcement off stops automatic limits.', observingSince: 'Observation began {time}',
    userDetail: 'User #{id}', limitSummary: 'Concurrency {concurrency} · RPM {rpm} · expires {until}', actionConfirm: 'Apply this action to the user? The action and reason will be recorded.',
    modeOff: 'Off', modeObserve: 'Observe', modeEnforce: 'Enforce', stateClear: 'No current alert', stateObserving: 'Observing', stateSuspected: 'Needs review', stateLimited: 'Limited',
    actionConfirmed: 'Confirm case', actionDismiss: 'Dismiss case', actionRestrict: 'Temporary manual limit', actionRevoke: 'Revoke limits',
    signalConcurrency: 'Concurrency saturation', signalRate: 'Request rate', signalTokens: 'Token throughput', signalRepeat: 'Exact repeat ratio', signalBreadth: 'Provider/model breadth', signalRegularity: 'Regular request gaps', signalOutput: 'Output amplification',
    users: 'Users', events: 'Events', exemptions: 'Exemptions', updated: 'Updated', eventType: 'Event', actor: 'Administrator',
    conflict: 'The record changed, or enforcement is not yet eligible. Refresh the data before trying again.', failed: 'Risk data is unavailable. Try again.', invalid: 'Enter a valid user ID, reason and an expiry within 30 days.', reasonRequired: 'Enter a reason before applying an action.', saved: 'Risk configuration or action saved'
  }
}
