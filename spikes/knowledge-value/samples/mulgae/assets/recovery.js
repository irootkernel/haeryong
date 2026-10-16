const prefix = 'Illustration — not an observed execution. ';

function showPrediction(panel, message, handles) {
  const output = panel.querySelector('[data-output]');
  const basis = panel.querySelector('[data-illustration-sources]');
  output.textContent = prefix + message;
  basis.replaceChildren(document.createTextNode('Basis: '));
  handles.forEach((handle, index) => {
    if (index) basis.append(document.createTextNode(', '));
    const link = document.createElement('a');
    link.href = '#source-' + handle;
    link.dataset.haeryongEvidence = handle;
    link.textContent = handle;
    basis.append(link);
  });
  basis.append(document.createTextNode('.'));
}

function inventoryPrediction(panel) {
  const source = panel.querySelector('[data-input=source]').value;
  const role = panel.querySelector('[data-input=role]').value;
  const proof = panel.querySelector('[data-input=proof]').value;
  const cleanup = panel.querySelector('[data-input=cleanup]').value;
  if (source === 'missing') {
    showPrediction(panel, 'Without the retained recovery source, this failed-run replay path is unavailable. The documented alternative is a new review when authorized; this control grants no authorization.', ['M-S09', 'M-S10']);
    return;
  }
  if (source === 'diagnostic') {
    showPrediction(panel, 'Historical diagnostic-only runs cannot be recovered retrospectively. Logs cannot be promoted into accepted results.', ['M-S09', 'M-S10']);
    return;
  }
  if (source === 'retained' && cleanup === 'mismatched') {
    const stage = proof === 'prepared'
      ? 'Preparation alone establishes no replay authority. The following refusal is conditional on later reaching the receipt comparison. '
      : 'This counterfactual replaces the assumed verified installation with a retention failure. ';
    showPrediction(panel, stage + 'Assuming preceding stages succeed and workspace release returns without error, a receipt that does not match completion evidence returns recovery unavailable: recovery_cleanup_receipt_invalid before PersistFailedRunRecovery. This path establishes no installed recovery source, so neither failed nor accepted attempt selection bypasses the refusal. Earlier failures may stop retention first. Retention refusal preserves the original execution failure; independently failing mandatory cleanup may affect the final failure class and exit, which this illustration does not predict.', ['M-S03', 'M-S10']);
    return;
  }
  if (source === 'retained' && proof === 'prepared') {
    showPrediction(panel, 'Preparation alone establishes no replay authority. Provider termination, target revalidation, workspace release, and installation of the recovery source are still required; this input does not establish available recovery. Assuming a matching receipt at a later comparison would pass only that check; subsequent diagnostics, context, and persistence can still fail.', ['M-S03', 'M-S10']);
    return;
  }
  if (source === 'retained' && role === 'accepted') {
    showPrediction(panel, 'The recovery-source loader rejects a succeeded source attempt: recovery rerun cannot replace an accepted role. Retention does not make that accepted attempt a retry target.', ['M-S02', 'M-S09']);
    return;
  }
  if (source === 'committed' && role === 'accepted') {
    showPrediction(panel, 'No committed-review replay-admission verdict is made for this selection. The supplied succeeded-attempt rejection is specifically in the recovery-source loader; the ordinary committed-review rerun admission implementation is outside these excerpts. The retention-stage and cleanup-receipt selections do not apply to this branch.', ['M-S02', 'M-S10']);
    return;
  }
  if (source === 'committed') {
    showPrediction(panel, 'The ADR admits committed reviews as rerun sources. Use the exact source run and failed attempt identity; the ordinary committed-review loader and full runtime admission checks are outside these excerpts. The retention-stage and cleanup-receipt selections do not apply to this branch.', ['M-S10']);
    return;
  }
  showPrediction(panel, 'With available verified failed-run recovery and a returned retry attempt, the documented next operation is exact replay of that run/attempt. A matching cleanup receipt alone does not establish availability; verified installation remains a separate assumption. Actual admission still depends on the real source and runtime checks.', ['M-S03', 'M-S09', 'M-S10']);
}

function composePrediction(panel) {
  const source = panel.querySelector('[data-input=source]').value;
  const mapping = panel.querySelector('[data-input=mapping]').value;
  const publication = panel.querySelector('[data-input=publication]').value;
  if (publication === 'cli') {
    showPrediction(panel, 'If a prior CLI compose publication returned reconciliation_state: status_required, inspect its exact returned composite run_id before repeating the mapping. The current source or mapping selections do not justify a blind retry.', ['M-S09']);
    return;
  }
  if (publication === 'mcp') {
    showPrediction(panel, 'If a prior MCP compose_review returned composite_publication_incomplete, inspect its exact returned run_id before repeating the mapping. The documented uncertain response includes deterministic session_id/run_id and retryable: false.', ['M-S09']);
    return;
  }
  if (source === 'root-missing') {
    showPrediction(panel, 'A root-read failure stops the shown service with CompositeValidationFailed: committed root is unavailable. A missing recovery source calls for a new review when authorized; diagnostics cannot replace it.', ['M-S05', 'M-S09', 'M-S10']);
    return;
  }
  if (source === 'ancestor-missing') {
    showPrediction(panel, 'If admission needs an intermediate lineage source and its read fails, verifyLineage returns CompositeLineageMismatch: recovery lineage source is unavailable. This model assumes that intermediate source is required by the selected chain.', ['M-S06']);
    return;
  }
  if (mapping === 'omit') {
    showPrediction(panel, 'Omitting the failed selected optional role is inadmissible. Every missing selected role needs a committed rerun regardless of Required. With no recovery selected at all, the request also violates the non-empty selection requirement; this is not a prediction of one exact first error.', ['M-S05', 'M-S10']);
    return;
  }
  if (mapping === 'replace') {
    showPrediction(panel, 'A recovery for the accepted required root role is rejected as CompositeRoleAlreadySatisfied. It also leaves the failed selected optional role unrecovered; replacing acceptance is not a way to complete this mapping.', ['M-S05', 'M-S06']);
    return;
  }
  if (mapping === 'mismatch') {
    showPrediction(panel, 'Assuming the candidate reaches the target-comparison check with otherwise valid preceding fields, a different target digest is rejected as CompositeTargetMismatch. A role-name match alone is insufficient.', ['M-S06']);
    return;
  }
  showPrediction(panel, 'Recovering the one failed selected optional role satisfies this scenario\'s missing-role selection, provided the committed rerun passes all admission checks. The accepted required role remains from the root. This predicts neither successful publication nor a CI pass.', ['M-S05', 'M-S06', 'M-S07', 'M-S10']);
}

for (const panel of document.querySelectorAll('[data-illustration]')) {
  const update = panel.dataset.illustration === 'inventory' ? inventoryPrediction : composePrediction;
  panel.addEventListener('change', () => update(panel));
  update(panel);
}
