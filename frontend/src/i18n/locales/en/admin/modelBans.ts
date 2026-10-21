export default {
  modelBans: {
    conflictReview: 'A newer version is saved. Compare it with your draft before reloading.',
    compareVersions: 'Compare versions',
    yourDraft: 'Your unsaved draft',
    latestSaved: 'Latest saved version',
    reloadLatest: 'Load latest version',
    discardDraftConfirm: 'Discard your unsaved edits and load the latest saved version?',

    evaluationContext: 'Evaluation context (optional)',
source: 'Source',allSources: 'All sources',manual: 'Manual',behavior: 'Behavior',
changeReason: 'Change reason',reasonRequired: 'A change reason is required.',protocol: 'Protocol',role: 'Model role',providerContext: 'Provider context',
    pending: 'Pending',
    action: 'Action', denyModel: 'Deny model', capConcurrency: 'Limit model concurrency', capRPM: 'Limit model RPM', limit: 'Limit (positive integer)', starts: 'Starts at (empty for immediate)',
    policies: 'Restrictions', selectUser: 'Select user', allStates: 'All states', active: 'Active', disabled: 'Disabled', expired: 'Expired', previous: 'Previous', next: 'Next', noPolicies: 'No matching model restrictions.', evaluate: 'Evaluate policy', allowed: 'Allowed', denied: 'Denied', unresolved: 'Unresolved identity',
    catalog: 'Model identity catalog', catalogHint: 'Register provider model identities and exact aliases used by each transport. Changes affect all users.', models: 'Models', displayName: 'Display name', aliases: 'Transport aliases', transport: 'Transport platform', alias: 'Exact alias',
    title: 'Model access restrictions', description: 'Manage a user’s model access across all groups and API keys.',
    crossGroup: 'Restrictions apply to every API key and group belonging to this user.',
    revision: 'Policy version {revision}', history: 'Change history', add: 'Add restriction', empty: 'No model restrictions for this user.',
    provider: 'Provider namespace', model: 'Canonical model', expires: 'Expires at (local time)', permanentHint: 'Leave empty for a permanent restriction.',
    reason: 'Reason', enabled: 'Enabled', clear: 'Clear manual restrictions', clearConfirm: 'Remove this user’s manual model restrictions? Behavior restrictions and change history will be retained.',
    exactHint: 'Use the exact provider namespace and model identifier. Wildcards and billing aliases are not supported.',
    saved: 'Model access policy saved', failed: 'Unable to load or save model access policy. Try again.',
    invalid: 'Enter a provider and exact model name for every rule. Remove duplicates and wildcards.',
    conflict: 'Another administrator changed this policy. Compare the latest version with your draft and confirm reloading before saving.',
    ruleCount: '{count} rules', viewVersion: 'View version', more: 'Load more', searchUser: 'Search user email or name', manage: 'Manage restrictions', user: 'User', noUsers: 'No matching users.'
  }
}
