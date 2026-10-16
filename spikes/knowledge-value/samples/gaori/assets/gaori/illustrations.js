const prefix = 'Illustration — not an observed execution. ';

function outcomePrediction(root) {
  const branch = root.querySelector('[data-input="branch"]').value;
  const childInput = root.querySelector('[data-input="child"]');
  childInput.disabled = branch === 'summarize-internal';
  if (branch === 'summarize-internal') {
    return prefix + 'Standalone summarize plus an extraction internal error, assuming safe materialization succeeds: summary/status status=internal_error, exit_code=4, extractor_status=degraded; Gaori CLI exit=4. No child is executed and no authoritative child execution result exists; the disabled child selection is not used. The materializationSummarizedRaw branch sets the artifact status and exit code, and ADR-0002 and the outcome matrix specify CLI exit 4. Failure and warning collections are cleared; their exact JSON array representation is not predicted.';
  }
  const child = childInput.value;
  if (branch === 'oversize') {
    return prefix + 'If the summary JSON write reaches the supplied size rejection, WriteSummaryJSON returns an artifact error classified as code 3, and the materializer returns an error instead of a completed runResult. A complete summary/status set is not established. The supplied excerpts do not show outer fatal CLI dispatch, so this control does not assert a numeric process exit for that failure. The earlier child exit ' + child + ' is not replaced by an invented successful artifact receipt.';
  }
  if (branch === 'miss') {
    if (child === '0') {
      return prefix + 'Child 0 plus a specialized parser miss, assuming no project rule matched and safe materialization succeeds: summary/status status=passed, exit_code=0, extractor_status=no_match; Gaori CLI exit=0. The specialized parser does not retry generic extraction. This is a miss branch, not an extraction internal error.';
    }
    return prefix + 'Child 7 plus a specialized parser miss, assuming no project rule matched and safe materialization succeeds: summary/status status=failed, exit_code=7, extractor_status=degraded; Gaori CLI exit=7. Missing a failure span does not erase the failed command result, and the specialized parser does not retry generic extraction.';
  }
  if (child === '0') {
    return prefix + 'Child 0 plus an extraction internal error, assuming safe materialization succeeds: summary/status status=internal_error, exit_code=0, extractor_status=degraded; Gaori CLI exit=4. Failure and warning collections are cleared; their exact JSON array representation is not predicted.';
  }
  return prefix + 'Child 7 plus the same extraction internal error, assuming safe materialization succeeds: summary/status status=failed, exit_code=7, extractor_status=degraded; Gaori CLI exit=7. Failure and warning collections are cleared, but the authoritative failed command result remains. Their exact JSON array representation is not predicted.';
}

function lifecyclePrediction(root) {
  const observation = root.querySelector('[data-input="observation"]').value;
  const sameSession = root.querySelector('[data-input="session"]').value === 'same';
  if (observation === 'finished') {
    return prefix + 'An actually observed phase=finished snapshot is terminal for that original invocation. Inspect its final result or error; finished does not mean passed. ' + (sameSession ? 'Awaiting the same ID in that original session returns the terminal snapshot immediately.' : 'The historical terminal observation remains meaningful, but a new server cannot recover the old session-local invocation ID. Reconcile retained final evidence without inventing a live invocation mapping.');
  }
  const observations = {
    wait: 'A wait expiry with changed:false does not settle command completion. Inspect the returned phase; it may already be finished because of concurrent completion.',
    await: 'The host deadline ends the await_run waiter only. This observation does not cancel or settle the command.',
    cancel: 'accepted:true records the first cancellation request for an unfinished invocation. It does not guarantee killed or stop artifact materialization. Completion still requires finished.',
    disconnect: 'Connection loss supplies no final command result. The guide requires reconciliation of the command and final artifacts before retrying.'
  };
  const continuation = sameSession
    ? (observation === 'disconnect'
      ? ' If the original server session is independently known to survive and access is restored, use its original ID to inspect or await completion. That survival is an input assumption, not a consequence of disconnection.'
      : ' If unfinished, inspect or await the same invocation in the known original server session; if finished, consume its final result or error.')
    : ' The original invocation registry is gone, replaced, or unverified. Do not assume an identical-looking ID in another server addresses the old run. Reconcile the original command and available final artifacts; missing artifacts alone do not prove it never executed.';
  return prefix + observations[observation] + continuation + ' Do not start a replacement merely because this observation occurred.';
}

for (const root of document.querySelectorAll('[data-illustration]')) {
  const output = root.querySelector('[data-output]');
  const predict = root.dataset.illustration === 'outcomes' ? outcomePrediction : lifecyclePrediction;
  const render = () => { output.textContent = predict(root); };
  root.addEventListener('change', render);
  render();
}
