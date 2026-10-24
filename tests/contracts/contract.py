"""Executable contract fixtures, not a service, durable ledger or importer.

The selected JSON Schema shapes in schema.json are the one machine field
authority. This small fixture validator implements only their named keywords;
unsupported keywords fail loudly. Cross-field rules below are exercised by
behavior tests. No listener, provider, credentials or production data is used.
"""
from copy import deepcopy
from pathlib import Path
from urllib.parse import urlparse
import hashlib
import io
import json
import math
import re
import unicodedata
import uuid
import zipfile
from urllib.parse import urlsplit
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import yaml

SCHEMA = json.loads(Path(__file__).with_name('schema.json').read_text())
UUID = re.compile(r'^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$')
KEY = re.compile(r'^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$')
ERROR_HTTP = SCHEMA['error_http']
# Positive leap days from the IERS table through Bulletin 72 (July 2026).
# The implementing parser owner updates announced facts; no future prediction.
UTC_LEAP_DAYS = frozenset('''1972-06-30 1972-12-31 1973-12-31 1974-12-31
1975-12-31 1976-12-31 1977-12-31 1978-12-31 1979-12-31 1981-06-30
1982-06-30 1983-06-30 1985-06-30 1987-12-31 1989-12-31 1990-12-31
1992-06-30 1993-06-30 1994-06-30 1995-12-31 1997-06-30 1998-12-31
2005-12-31 2008-12-31 2012-06-30 2015-06-30 2016-12-31'''.split())


class ContractError(ValueError):
    def __init__(self, code, field='', reason=''):
        self.code, self.field, self.reason = code, field, reason
        self.http = ERROR_HTTP[code]
        super().__init__(f'{code} {field}: {reason}')


def fail(code='INVALID_REQUEST', field='', reason=''):
    raise ContractError(code, field, reason)


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True,
                      separators=(',', ':'), allow_nan=False).encode('utf-8')


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def path(value):
    if (not isinstance(value, str) or not value or value.startswith('/')
            or re.match(r'^[A-Za-z]:', value) or '\x00' in value
            or any(part in ('', '.', '..') for part in value.split('/'))):
        fail('UNSAFE_PATH', '/path', 'relative exact path required')
    try:
        value.encode('utf-8')
    except UnicodeEncodeError:
        fail('UNSAFE_PATH', '/path', 'invalid UTF-8 name')
    return value


def origin(value, kind='origin'):
    try:
        parts = urlsplit(value)
        port = parts.port
    except (ValueError, TypeError):
        return False
    valid = (parts.scheme in ('http', 'https') and bool(parts.hostname)
             and parts.username is None and parts.password is None
             and parts.path == '' and not parts.query and not parts.fragment
             and '*' not in value and value == value.strip())
    if kind == 'https-origin':
        valid = valid and parts.scheme == 'https'
    if kind == 'loopback-origin':
        valid = valid and parts.scheme == 'http' and parts.hostname == '127.0.0.1'
    return valid and (port is None or 1024 <= port <= 65535)


def utc_time(value):
    match = re.fullmatch(r'([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.[0-9]+)?Z', value)
    if match is None:
        return False
    year, month, day, hour, minute, second = map(int, match.groups())
    leap = year % 4 == 0 and (year % 100 != 0 or year % 400 == 0)
    days = (31, 29 if leap else 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31)
    if not (1 <= month <= 12 and 1 <= day <= days[month - 1] and hour <= 23 and minute <= 59):
        return False
    return second <= 59 or (second == 60 and hour == 23 and minute == 59 and value[:10] in UTC_LEAP_DAYS)


def validate(name, value, code='INVALID_REQUEST'):
    """Validate selected shapes; error pointers always identify submitted fields."""
    if name == 'config' and isinstance(value, dict) and value.get('schema_version') not in (None, '1.0'):
        fail('UNSUPPORTED_SCHEMA', '/schema_version')
    if name in ('personal_reader_preference', 'personal_reading_position', 'personal_page_position') and isinstance(value, dict):
        absent = value.get('exists') is False
        if absent:
            if not (value.get('revision') is None and value.get('etag') is None):
                fail(field='/revision', reason='absent personal state carries null validators')
        elif value.get('revision') is None or value.get('etag') is None:
            fail(field='/revision', reason='stored personal state carries real validators')
        if ('required_precondition' in value) is not absent:
            fail(field='/required_precondition', reason='create precondition only represents absent personal state')
        family_defaults = {
            'personal_reader_preference': {'default_page': None},
            'personal_reading_position': {'page_path': None, 'page_query': '', 'page_fragment': '', 'opened': False, 'first_opened_at': None, 'last_opened_at': None},
            'personal_page_position': {'page_query': '', 'page_fragment': '', 'position': {}},
        }
        if absent:
            if value.get('value') != family_defaults[name]:
                fail(field='/value', reason='absent personal state carries the exact family default value')
            if value.get('page_position_url') is not None:
                fail(field='/page_position_url', reason='absent personal state links no page-position resource')
        elif name == 'personal_reading_position':
            stored = value.get('value')
            page = stored.get('page_path') if isinstance(stored, dict) else None
            link = value.get('page_position_url')
            if page is None and link is not None:
                fail(field='/page_position_url', reason='null-page pointer links no page-position resource')
            if page is not None and not (isinstance(link, str) and link):
                fail(field='/page_position_url', reason='stored pointer with a page links its page-position resource')
        elif value.get('page_position_url') is not None:
            fail(field='/page_position_url', reason='only the reading pointer carries a page-position link')
    if name == 'upload_create' and isinstance(value, dict) and value.get('purpose') in ('body', 'metadata'):
        target = value.get('target', {})
        choices = ['question', 'annotation_create', 'annotation_edit', 'generation', 'evidence_verification'] if value['purpose'] == 'body' else ['import', 'draft_replace']
        if isinstance(target, dict) and target.get('action') not in choices:
            fail(field='/target/action', reason='unknown action-specific target')
    def walk(rule, data, at):
        allowed = {'$ref', 'anyOf', 'oneOf', 'type', 'properties', 'required',
                   'additionalProperties', 'items', 'enum', 'minLength',
                   'minimum', 'maximum', 'maxLength', 'pattern', 'format'}
        assert set(rule) <= allowed, set(rule) - allowed
        if '$ref' in rule:
            return walk(SCHEMA['$defs'][rule['$ref'].split('/')[-1]], data, at)
        for union in ('anyOf', 'oneOf'):
            if union in rule:
                successes, errors = 0, []
                for candidate in rule[union]:
                    try:
                        walk(candidate, data, at)
                        successes += 1
                    except ContractError as error:
                        errors.append(error)
                if successes and (union == 'anyOf' or successes == 1):
                    return
                # Prefer the discriminator-matching closed variant's field error.
                if isinstance(data, dict):
                    for candidate in rule[union]:
                        resolved = SCHEMA['$defs'][candidate['$ref'].split('/')[-1]] if '$ref' in candidate else candidate
                        props = resolved.get('properties', {})
                        discriminator = [k for k in ('purpose', 'action') if k in props]
                        if discriminator and all(data.get(k) in props[k].get('enum', []) for k in discriminator):
                            return walk(candidate, data, at)
                if errors:
                    raise max(errors, key=lambda e: len(e.field))
                fail(code, at, 'union mismatch')
        types = {'object': dict, 'array': list, 'string': str, 'integer': int,
                 'boolean': bool, 'null': type(None)}
        if rule.get('type') == 'number':
            if type(data) not in (int, float) or (type(data) is float and not math.isfinite(data)):
                fail(code, at, 'expected finite JSON number')
        elif 'type' in rule and type(data) is not types[rule['type']]:
            fail(code, at, 'expected ' + rule['type'])
        if 'enum' in rule and not any(type(data) is type(v) and data == v for v in rule['enum']):
            fail('INVALID_ENUM' if code == 'INVALID_REQUEST' else code, at, 'unknown value')
        if isinstance(data, dict):
            props = rule.get('properties', {})
            for key in rule.get('required', []):
                if key not in data:
                    fail(code, at + '/' + key, 'required field omitted')
            for key, item in data.items():
                if key in props:
                    walk(props[key], item, at + '/' + key)
                elif rule.get('additionalProperties') is False:
                    fail(code, at + '/' + key, 'unknown field')
                elif isinstance(rule.get('additionalProperties'), dict):
                    walk(rule['additionalProperties'], item, at + '/' + key)
        if isinstance(data, list) and 'items' in rule:
            for i, item in enumerate(data):
                walk(rule['items'], item, at + '/' + str(i))
        if isinstance(data, str):
            if len(data) > rule.get('maxLength', len(data)):
                fail(code, at, 'control string too long')
            if len(data) < rule.get('minLength', 0):
                fail(code, at, 'empty string')
            if 'pattern' in rule and not re.fullmatch(rule['pattern'], data):
                fail(code, at, 'invalid representation')
            fmt = rule.get('format')
            if fmt == 'uuid' and not UUID.fullmatch(data):
                fail(code, at, 'UUID-form identifier required')
            if fmt == 'relative-path':
                try:
                    path(data)
                except ContractError:
                    fail(code, at, 'unsafe relative path')
            if fmt and fmt.endswith('origin') and not origin(data, fmt):
                fail(code, at, 'exact validated origin required')
            if fmt == 'utc-time' and not utc_time(data):
                fail(code, at, 'UTC RFC3339 required')
        if type(data) is int:
            if data < rule.get('minimum', data) or data > rule.get('maximum', data):
                fail(code, at, 'number outside typed range')
    walk(SCHEMA['$defs'][name], value, '')
    if name in ('reference_from_view', 'reference_exact_target', 'backlink_scope', 'backlink_endpoint'):
        if value['region_id'] is not None and value['page_path'] is None:
            fail(code, '/page_path', 'region requires exact page')
    if name == 'reference_view':
        if value['resolution'] == 'unresolved' and value['reader_url'] is not None:
            target = value['target']
            if target is None or (target['page_path'] is not None and target['resolved_path'] is None):
                fail(code, '/reader_url', 'fallback requires an authorized exact page or version')
        validate('reference_from_view', value['from'], code)
        if value['target'] is not None and value['resolution'] != 'tombstoned':
            validate('reference_exact_target', value['target'], code)
    if name in ('reference_page', 'reference_response', 'reference_resolution_response'):
        if name == 'reference_page':
            for row in value['items']:
                validate('reference_view', row, code)
        elif name == 'reference_response':
            validate('reference_page', value['data'], code)
        else:
            validate('reference_view', value['data'], code)
    if name == 'backlink_view':
        validate('backlink_endpoint', value['from'], code)
        validate('backlink_endpoint', value['target'], code)
    if name == 'backlink_page':
        validate('backlink_scope', value['scope'], code)
        for row in value['items']:
            validate('backlink_view', row, code)
    if name == 'backlink_response':
        validate('backlink_page', value['data'], code)
    if name == 'explanation_reference' and value['region_id'] is not None and value['page_path'] is None:
        fail(code, '/page_path', 'region requires an exact page')
    return value


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                fail(field='/' + key, reason='duplicate key')
            result[key] = value
        return result
    try:
        return json.loads(raw, object_pairs_hook=pairs,
                          parse_constant=lambda _: fail(reason='nonfinite JSON'))
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        fail(reason=str(error))


def strict_yaml(raw):
    # Reject anchors, aliases, merge keys and explicit tags before constructing.
    try:
        for event in yaml.parse(raw):
            if isinstance(event, yaml.events.AliasEvent) or getattr(event, 'anchor', None) or getattr(event, 'tag', None):
                fail(reason='YAML tags/anchors/aliases are unsupported')
        class Unique(yaml.SafeLoader):
            pass
        def mapping(loader, node):
            result = {}
            for key_node, value_node in node.value:
                key = loader.construct_object(key_node)
                if not isinstance(key, str) or key == '<<' or key in result:
                    fail(reason='duplicate, merge or nonstring YAML key')
                result[key] = loader.construct_object(value_node)
            return result
        Unique.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, mapping)
        return yaml.load(raw, Loader=Unique)
    except yaml.YAMLError as error:
        fail(reason=str(error))


def configuration(value):
    validate('config', value)
    schedule = value['maintenance']['schedule']
    try:
        ZoneInfo(schedule['timezone'])
    except (ZoneInfoNotFoundError, ValueError):
        fail(field='/maintenance/schedule/timezone', reason='unknown IANA timezone')
    mode, interval, cron = (schedule[k] for k in ('mode', 'interval_seconds', 'cron'))
    if ((mode == 'manual' and (interval is not None or cron is not None))
            or (mode == 'interval' and (interval is None or cron is not None))
            or (mode == 'calendar' and (interval is not None or cron is None))):
        fail(field='/maintenance/schedule', reason='mode-specific fields conflict')
    if cron is not None:
        fields = cron.split()
        bounds = [(0, 59), (0, 23), (1, 31), (1, 12), (0, 6)]
        if len(fields) != 5:
            fail(field='/maintenance/schedule/cron', reason='five fields required')
        for term, (low, high) in zip(fields, bounds):
            for part in term.split(','):
                match = re.fullmatch(r'(\*|\d+(?:-\d+)?)(?:/(\d+))?', part)
                if not match or (match[2] is not None and int(match[2]) < 1):
                    fail(field='/maintenance/schedule/cron', reason='invalid cron term')
                if match[1] != '*':
                    values = list(map(int, match[1].split('-')))
                    if not (low <= min(values) <= max(values) <= high) or values != sorted(values):
                        fail(field='/maintenance/schedule/cron', reason='cron range outside field')
    network = value['network']
    if network['remote_enabled'] and network['external_base_url'] is None:
        fail(field='/network/external_base_url', reason='remote HTTPS origin required')
    trusted = set(network['allowed_origins'])
    local = 'http://127.0.0.1:' + str(network['listener']['port'])
    if trusted != {local} | ({network['external_base_url']} if network['remote_enabled'] else set()):
        fail(field='/network/allowed_origins', reason='exact configured management endpoints only')
    mappings = network.get('renderer_mappings', [])
    origins = [m['origin'] for m in mappings]
    if len(set(origins)) != len(origins) or set(origins) & trusted:
        fail(field='/network/renderer_mappings', reason='unique separate quarantined origins required')
    pool = network.get('renderer_pool')
    if pool is not None:
        base, count = pool['base_port'], pool['count']
        if base + count - 1 > 65535:
            fail(field='/network/renderer_pool/count', reason='range exceeds the port space')
        if base <= 9273 and base + count - 1 >= 9272:
            fail(field='/network/renderer_pool/base_port', reason='range intersects the frozen legacy fixture origins 9272-9273')
        if base <= network['listener']['port'] <= base + count - 1:
            fail(field='/network/renderer_pool', reason='range must not include the management listener port')
        for mapping in mappings:
            upstream = urlparse(mapping['upstream'])
            if upstream.port is not None and base <= upstream.port <= base + count - 1:
                fail(field='/network/renderer_pool', reason='range must not include a renderer mapping upstream port')
    for project_id, settings in value['projects'].items():
        validate('id', project_id)
        if 'maintenance' in settings:
            inherited = deepcopy(value)
            inherited['projects'] = {}
            inherited['maintenance'] = merge_configuration(value['maintenance'], settings['maintenance'])
            configuration(inherited)
        for source in settings.get('sources', []):
            if source['kind'] == 'git' and source['repo_id'] is None:
                fail(field='/projects/' + project_id + '/sources', reason='Git repository identity required')
    return deepcopy(value)


def merge_configuration(current, changes):
    result = deepcopy(current)
    for key, value in changes.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = merge_configuration(result[key], value)
        else:
            result[key] = deepcopy(value)
    return result


def access_projection(policy):
    return {'source': policy.get('source'), 'projects': {
        project: {key: settings[key] for key in ('sources', 'output_destination') if key in settings}
        for project, settings in policy.get('projects', {}).items()
        if any(key in settings for key in ('sources', 'output_destination'))}}


def metadata(value, entries):
    if value is None:
        return {'source_basis': 'unverified', 'source_intent': None}
    validate('metadata', value, 'PACKAGE_INVALID')
    sources, evidence = value.get('sources', []), value.get('evidence', [])
    for collection in ('sources', 'evidence', 'references'):
        ids = [row['id'] for row in value.get(collection, [])]
        if len(ids) != len(set(ids)):
            fail('PACKAGE_INVALID', '/' + collection, 'duplicate local ID')
    source_ids = {row['id'] for row in sources}
    evidence_ids = {row['id'] for row in evidence}
    for i, source in enumerate(sources):
        at = '/sources/' + str(i)
        if source['kind'] == 'git':
            if not all(k in source for k in ('repo_id', 'object_format', 'exact_revision')):
                fail('INVALID_EVIDENCE', at, 'complete Git identity required')
            length = 40 if source['object_format'] == 'sha1' else 64
            if not re.fullmatch('[0-9a-f]{' + str(length) + '}', source['exact_revision']):
                fail('INVALID_EVIDENCE', at + '/exact_revision', 'full exact object identity required')
            if 'snapshot_sha256' in source:
                fail('INVALID_EVIDENCE', at + '/snapshot_sha256', 'Git and snapshot identity are distinct')
        elif 'snapshot_sha256' not in source:
            fail('INVALID_EVIDENCE', at, 'snapshot identity required')
        elif any(key in source for key in ('repo_id', 'object_format', 'exact_revision')):
            fail('INVALID_EVIDENCE', at, 'snapshot cannot claim Git fields')
        if source['retention_mode'] == 'selected_snapshot':
            asset = source.get('asset_path')
            if asset not in entries or entries[asset][0] != 'file':
                fail('INVALID_EVIDENCE', at + '/asset_path', 'inventoried bytes required')
            expected = source.get('bytes_sha256', source.get('snapshot_sha256'))
            if expected is None or digest(entries[asset][1]) != expected:
                fail('INVALID_EVIDENCE', at + '/snapshot_sha256', 'retained bytes mismatch')
    for i, item in enumerate(evidence):
        if item['source_id'] not in source_ids:
            fail('INVALID_EVIDENCE', '/evidence/' + str(i) + '/source_id', 'unknown local source')
        if ('line_start' in item) != ('line_end' in item) or item.get('line_start', 1) > item.get('line_end', 1):
            fail('INVALID_EVIDENCE', '/evidence/' + str(i), 'invalid bounded range')
    seen = set()
    for i, region in enumerate(value.get('regions', [])):
        key = (region['page_path'], region['id'])
        if key in seen or not set(region.get('evidence_ids', [])) <= evidence_ids:
            fail('PACKAGE_INVALID', '/regions/' + str(i), 'duplicate region or unknown evidence')
        seen.add(key)
    for i, reference in enumerate(value.get('references', [])):
        if reference['region_id'] is not None and reference['page_path'] is None:
            fail('PACKAGE_INVALID', '/references/' + str(i), 'region requires exact page')
    return deepcopy(value)


def inventory(entries):
    """entries: exact path -> (file, bytes), (directory, None), (symlink, text)."""
    rows = {}
    for name, entry in entries.items():
        path(name)
        kind, content = entry
        if kind == 'file':
            if not isinstance(content, bytes):
                fail('PACKAGE_INVALID', name, 'file bytes required')
            row = [name, kind, str(len(content)), digest(content)]
        elif kind == 'directory':
            row = [name, kind]
        elif kind == 'symlink' and isinstance(content, str):
            row = [name, kind, content]
        else:
            fail('PACKAGE_INVALID', name, 'unknown entry representation')
        if name in rows and rows[name] != row:
            fail('INPUT_CHANGED', name, 'entry conflict')
        rows[name] = row
        parts = name.split('/')
        for count in range(1, len(parts)):
            parent = '/'.join(parts[:count])
            if parent in rows and rows[parent] != [parent, 'directory']:
                fail('PACKAGE_INVALID', parent, 'file/link parent conflict')
            rows[parent] = [parent, 'directory']
    return b''.join(json.dumps(rows[name], ensure_ascii=False, separators=(',', ':')).encode('utf-8') + b'\n'
                    for name in sorted(rows, key=lambda text: text.encode('utf-8')))


def capture(selection, kind):
    """Fixture capture only; not streamed production import/export."""
    if kind == 'directory':
        return deepcopy(selection)
    if kind == 'file':
        return {path(selection[0]): ('file', selection[1])}
    if kind != 'archive':
        fail('PACKAGE_INVALID', reason='unknown intake kind')
    result = {}
    try:
        with zipfile.ZipFile(io.BytesIO(selection)) as archive:
            for item in archive.infolist():
                name = item.filename[:-1] if item.is_dir() else item.filename
                path(name)
                mode = item.external_attr >> 16
                value = ('directory', None) if item.is_dir() else ('symlink', archive.read(item).decode('utf-8')) if mode & 0o170000 == 0o120000 else ('file', archive.read(item))
                if name in result and result[name] != value:
                    fail('PACKAGE_INVALID', name, 'conflicting duplicate ZIP entry')
                result[name] = value
    except (zipfile.BadZipFile, UnicodeDecodeError) as error:
        fail('PACKAGE_INVALID', reason=str(error))
    inventory(result)
    return result


def lookup(entries, requested, resolve_link=False):
    path(requested)
    original, seen = requested, set()
    while True:
        actual = requested
        if actual not in entries:
            equivalent = [p for p in entries if unicodedata.normalize('NFC', p) == unicodedata.normalize('NFC', actual)]
            if len(equivalent) > 1:
                fail(field='/path', reason='ambiguous_path')
            if not equivalent:
                fail('NOT_FOUND', '/path', 'unresolved_link' if seen else 'unknown path')
            actual = equivalent[0]
        kind, data = entries[actual]
        if kind != 'symlink' or not resolve_link:
            return {'requested_path': original, 'resolved_path': actual, 'kind': kind, 'data': data}
        if actual in seen:
            fail('NOT_FOUND', '/path', 'unresolved_link')
        seen.add(actual)
        # Resolve link text against its parent, allowing .. only inside snapshot.
        if data.startswith('/') or re.match(r'^[A-Za-z]:', data):
            fail('NOT_FOUND', '/path', 'unresolved_link')
        parts = actual.split('/')[:-1]
        for part in data.split('/'):
            if part == '..':
                if not parts:
                    fail('NOT_FOUND', '/path', 'unresolved_link')
                parts.pop()
            elif part not in ('', '.'):
                parts.append(part)
        requested = '/'.join(parts)


def context_binding(pairs):
    values = {}
    for key, value in pairs:
        if key not in ('version_id', 'page_path', 'render_instance_id') or key in values or not value:
            fail(field='/' + key, reason='unknown/duplicate/empty query binding')
        values[key] = value
    if 'version_id' not in values:
        fail(field='/version_id', reason='required')
    values.setdefault('page_path', None)
    values.setdefault('render_instance_id', None)
    validate('context_target', values)
    return values


def citation(value, observations, verifications, attempt_id=None):
    validate('citation', value)
    source = value['source']
    if source['kind'] == 'git':
        length = 40 if source['object_format'] == 'sha1' else 64
        if source['repo_id'] is None or source['object_format'] is None or not re.fullmatch('[0-9a-f]{' + str(length) + '}', source['exact_revision'] or '') or source['snapshot_sha256'] is not None:
            fail('INVALID_EVIDENCE', '/source', 'complete exact Git identity required')
    elif source['snapshot_sha256'] is None or any(source[k] is not None for k in ('repo_id', 'object_format', 'exact_revision')):
        fail('INVALID_EVIDENCE', '/source', 'exact snapshot identity required')
    identity = {k: value[k] for k in ('target_set_id', 'source', 'location', 'bytes_sha256')}
    if value['basis'] == 'observed_input':
        observation = observations.get(value['read_id'])
        if value['verification_id'] is not None or not observation or (attempt_id is not None and observation.get('attempt_id') != attempt_id) or observation.get('input_origin') != value['input_origin'] or any(observation.get(k) != v for k, v in identity.items()):
            fail('INVALID_EVIDENCE', '/read_id', 'actual producing-attempt exact input observation required')
    else:
        verification = verifications.get(value['verification_id'])
        if value['read_id'] is not None or value['input_origin'] is not None or not verification or verification.get('outcome') not in ('captured', 'matched') or not verification.get('verified_at') or any(verification.get(k) != v for k, v in identity.items()):
            fail('INVALID_EVIDENCE', '/verification_id', 'successful later exact target check required')
    return deepcopy(value)


def association(remembered, explicit, documents, owner=True, unfiled=None):
    """Client-only precedence. The API never applies remembered associations."""
    if explicit.get('new_document') and explicit.get('document_id'):
        fail(field='/document_id', reason='new-document conflicts with explicit document')
    document = explicit.get('document_id')
    if document is None and not explicit.get('new_document') and 'project_id' not in explicit:
        document = remembered.get('document_id')
    project = explicit.get('project_id', remembered.get('project_id'))
    if document is not None:
        if document not in documents:
            fail('NOT_FOUND')
        actual_project = documents[document]
        if 'project_id' in explicit and project != actual_project:
            fail(field='/project_id', reason='document belongs to another project')
        project = actual_project
    if project is None:
        if not owner or unfiled is None:
            fail('NOT_FOUND')
        project = unfiled
    return {'project_id': project, 'document_id': document}


def recover_journal(journal, server_operation):
    """A missing server receipt cannot erase persisted caller intent or phase."""
    required = {'final_key', 'create_key', 'seal_key', 'target', 'base', 'phase'}
    if not required <= journal.keys():
        fail('OPERATION_RECONCILIATION_REQUIRED', reason='caller history incomplete')
    return {'journal': deepcopy(journal), 'server_operation': deepcopy(server_operation),
            'new_dispatch_authorized': False,
            'publication_known_absent': False}


def coverage_status(target_available, eligible, outcomes, discrepancy=False):
    if not target_available:
        return 'unable_to_verify'
    if discrepancy:
        return 'outdated_against_target'
    if 'blocked' in outcomes:
        return 'unable_to_verify'
    return 'aligned_within_scope' if eligible > 0 and len(outcomes) == eligible and all(x == 'assessed' for x in outcomes) else 'not_assessed'


def compatibility(version, required_fields, response, known_optional=()):
    if version != SCHEMA['runtime_schema_version']:
        fail('UNSUPPORTED_SCHEMA', '/schema_version')
    if not set(required_fields) <= response.keys():
        fail('INVALID_STATE_SCHEMA', reason='required response meaning absent')
    for field in ('state', 'action', 'authority'):
        if field in response and response[field] not in required_fields.get(field, []):
            fail('INVALID_STATE_SCHEMA', '/' + field, 'unknown authority-bearing meaning')
    return {k: v for k, v in response.items() if k in required_fields or k in known_optional}


class Model:
    """Deliberately in-memory contract model; no durability/crash proof."""
    def __init__(self):
        self.uploads, self.receipts, self.jobs, self.versions = {}, {}, {}, {}
        self.latest, self.plans = {}, {}
        self.config_revision = self.permissions_revision = 1
        self.policy = {}
        self.document_titles = {}
        self.objects = {}

    def authorize(self, authority, scope):
        if authority != 'owner' and scope not in authority:
            fail('FORBIDDEN', reason='current action authority required')

    def request(self, key, route, body, effect, authority='owner', scope='import.publish'):
        self.authorize(authority, scope)
        if key is None:
            fail('PRECONDITION_REQUIRED', '/Idempotency-Key', 'Idempotency-Key')
        if not isinstance(key, str) or not KEY.fullmatch(key):
            fail(field='/Idempotency-Key', reason='one lowercase unquoted UUID required')
        fingerprint = digest(canonical({'method': 'POST', 'route': route, 'body': body}))
        if key in self.receipts:
            receipt = self.receipts[key]
            if 'plan_id' in receipt or receipt['fingerprint'] != fingerprint:
                fail('OPERATION_CONFLICT')
            return deepcopy(receipt['result'])
        saved = deepcopy(self.__dict__)
        try:
            result = effect()
            self.receipts[key] = {'fingerprint': fingerprint, 'result': deepcopy(result)}
            return result
        except ContractError:
            self.__dict__.update(saved)
            raise

    def create(self, key, body, authority='owner'):
        validate('upload_create', body)
        target, purpose = body['target'], body['purpose']
        scope = ('draft.write' if target.get('draft_id') else 'import.publish') if purpose == 'result' else 'context.write' if purpose == 'context' else {'question': 'owner', 'annotation_create': 'owner', 'annotation_edit': 'owner', 'generation': 'generation.request', 'evidence_verification': 'source.read', 'import': 'import.publish', 'draft_replace': 'draft.write'}[target['action']]
        def effect():
            upload_id = str(uuid.uuid4())
            target_copy = deepcopy(target)
            if purpose == 'result':
                target_copy.setdefault('draft_id', None)
            self.uploads[upload_id] = {'id': upload_id, 'purpose': purpose, 'target': target_copy, 'input_mode': body['input_mode'], 'selection_name': body.get('selection_name'), 'state': 'open', 'bytes': b'', 'checkpoint': {'generation': 1, 'inventory_sha256': digest(b''), 'accepted_bytes': '0', 'complete_input': False}, 'job': None, 'generation': 1, 'adopted': False, 'pins': {'staging': 1, 'adopted': 0, 'active_work': 0, 'recovery': 0}, 'available': True, 'normalization': {}, 'revision': 1}
            return upload_id
        return self.request(key, '/uploads', body, effect, authority, scope)

    def write(self, upload_id, raw, checkpoint):
        upload = self.uploads[upload_id]
        validate('checkpoint', checkpoint)
        if upload['state'] != 'open':
            fail('INVALID_TRANSITION')
        if checkpoint['accepted_bytes'] != str(len(raw)) or checkpoint['inventory_sha256'] != digest(raw):
            fail('HASH_MISMATCH')
        if upload['bytes'] != raw or upload['checkpoint'] != checkpoint:
            upload['revision'] += 1
        upload['bytes'], upload['checkpoint'] = raw, deepcopy(checkpoint)

    def complete(self, key, upload_id, checkpoint):
        validate('checkpoint', checkpoint)
        def effect():
            upload = self.uploads[upload_id]
            if upload['state'] == 'abandoned' or upload['checkpoint'] != checkpoint or not checkpoint['complete_input']:
                fail('INVALID_TRANSITION')
            if upload['state'] == 'sealed':
                return {'status': 200, 'upload_id': upload_id}
            if upload['state'] == 'open':
                job = str(uuid.uuid4())
                upload['state'], upload['job'] = 'sealing', job
                upload['revision'] += 1
                upload['pins']['active_work'] = 1
                self.jobs[job] = {'state': 'running', 'generation': upload['generation'], 'cancel': False}
            return {'status': 202, 'job_id': upload['job']}
        return self.request(key, '/uploads/' + upload_id + '/complete', {'checkpoint': checkpoint}, effect)

    def settle(self, upload_id, generation, success=True, stopped=False):
        upload = self.uploads[upload_id]
        job = self.jobs[upload['job']]
        if upload['state'] == 'abandoned' or job['cancel'] or generation != upload['generation']:
            if stopped:
                job['state'] = 'cancelled'
                upload['pins']['active_work'] = 0
                if upload['state'] != 'abandoned':
                    upload['state'] = 'open'
                    upload['revision'] += 1
            return False
        job['state'] = 'completed' if success else 'failed'
        upload['state'] = 'sealed' if success else 'open'
        upload['revision'] += 1
        upload['pins']['active_work'] = 0
        if success and upload['purpose'] in ('context', 'body', 'metadata'):
            object_id = upload.setdefault('object_id', str(uuid.uuid4()))
            self.objects[object_id] = upload_id
        elif success:
            upload.setdefault('content_set_id', str(uuid.uuid4()))
        return success

    def cancel(self, upload_id):
        upload = self.uploads[upload_id]
        job = self.jobs[upload['job']]
        if job['state'] == 'completed':
            return 'already_completed'
        upload['generation'] += 1
        job['state'], job['cancel'] = 'cancelling', True
        return 'cancelling'

    def abandon(self, upload_id):
        upload = self.uploads[upload_id]
        if upload['job'] and self.jobs[upload['job']]['state'] not in ('completed', 'failed', 'cancelled'):
            self.cancel(upload_id)
        if upload['state'] != 'abandoned':
            upload['state'] = 'abandoned'
            upload['revision'] += 1

    def resume(self, job_id):
        job = self.jobs[job_id]
        if job['cancel'] or job['state'] in ('completed', 'failed', 'cancelled'):
            fail('INVALID_TRANSITION', '/job', 'cancel intent/terminal job cannot resume')
        return job['state']

    def reusable(self, upload_id, authorized=True):
        upload = self.uploads[upload_id]
        return upload['state'] == 'sealed' and upload['available'] and authorized

    def adopt_object(self, upload_id, target, operation, authority='owner'):
        upload = self.uploads[upload_id]
        action = target.get('action')
        required = 'context.write' if upload['purpose'] == 'context' else 'generation.request' if action == 'generation' else 'source.read' if action == 'evidence_verification' else 'import.publish' if action == 'import' else 'draft.write' if action == 'draft_replace' else 'owner'
        self.authorize(authority, required)
        if not self.reusable(upload_id) or upload['target'] != target or (target.get('operation_id') is not None and target['operation_id'] != operation):
            fail(field='/target', reason='exact consumer binding required')
        upload['adopted'] = True
        upload['pins']['adopted'] += 1
        return digest(upload['bytes'])

    def object_reference(self, upload_id):
        upload = self.uploads[upload_id]
        if not self.reusable(upload_id) or 'object_id' not in upload:
            fail('INVALID_TRANSITION')
        return {'object_id': upload['object_id'], 'bytes_sha256': digest(upload['bytes']),
                'byte_size': str(len(upload['bytes'])), 'media_type': 'application/json'}

    def replace_draft(self, upload_id, draft):
        upload = self.uploads[upload_id]
        expected = {k: draft[k] for k in ('project_id', 'document_id', 'draft_id')}
        if draft['state'] != 'ready' or upload['target'] != expected or not self.reusable(upload_id):
            fail(field='/upload_id', reason='exact ready draft target required')
        draft['history'].append(digest(upload['bytes']))
        draft['revision'] += 1

    def import_result(self, key, body, authority='owner', reserve_only=False):
        self.authorize(authority, 'import.publish')
        validate('import_request', body)
        if 'metadata' in body and 'metadata_ref' in body:
            fail(field='/metadata_ref', reason='mutually exclusive metadata inputs')
        upload = self.uploads[body['upload_id']]
        target = upload['target']
        if upload['purpose'] != 'result' or target.get('draft_id') is not None or body['document_id'] != target['document_id'] or body.get('project_id', target['project_id']) != target['project_id']:
            fail(field='/document_id', reason='exact publication target required')
        if body['document_id'] is not None and body.get('base_version_id', 'omitted') is None:
            fail(field='/base_version_id', reason='existing document needs ID or omission')
        if body['document_id'] is None and body.get('base_version_id') is not None:
            fail(field='/base_version_id', reason='new document base must be null')
        semantic = {k: v for k, v in body.items() if k != 'upload_id'}
        if 'metadata_ref' in body:
            reference = body['metadata_ref']
            object_upload_id = self.objects.get(reference['object_id'])
            if object_upload_id is None:
                fail(field='/metadata_ref', reason='unknown sealed metadata object')
            object_upload = self.uploads[object_upload_id]
            expected_target = {'operation_id': key, 'action': 'import', 'project_id': target['project_id'], 'document_id': body['document_id']}
            if object_upload['purpose'] != 'metadata' or object_upload['target'] != expected_target or self.object_reference(object_upload_id) != reference:
                fail(field='/metadata_ref', reason='exact metadata consumer and bytes required')
            value = strict_json(object_upload['bytes'])
            validate('metadata', value, 'PACKAGE_INVALID')
            semantic.pop('metadata_ref')
            semantic['metadata'] = value
        semantic['resolved_project_id'] = target['project_id']
        semantic['payload_sha256'] = digest(upload['bytes'])
        if body['document_id'] is None and 'title' not in semantic.get('metadata', {}).get('document', {}):
            semantic['selection_name_used_for_default'] = upload['selection_name']
        def effect():
            if not self.reusable(body['upload_id']):
                fail('INVALID_TRANSITION')
            title = semantic.get('metadata', {}).get('document', {}).get('title', self.document_titles.get(body['document_id'], upload['selection_name'] or ''))
            normalized = {'project_id': target['project_id'], 'document_id': body['document_id'] or str(uuid.uuid4()), 'version_id': body.get('version_id', str(uuid.uuid4())), 'base_version_id': body.get('base_version_id', self.latest.get(body['document_id'])), 'created_at': '2026-10-16T00:00:00Z', 'title_snapshot': title, 'omitted_fields': sorted(set(SCHEMA['$defs']['import_request']['properties']) - body.keys()), 'schema_version': '1.0', 'normalizer_version': '1.0'}
            validate('normalization_context', normalized)
            for section, field, identity in (
                    ('document', 'id', 'document_id'),
                    ('document', 'project_id', 'project_id'),
                    ('version', 'id', 'version_id'),
                    ('version', 'base_version_id', 'base_version_id')):
                submitted = semantic.get('metadata', {}).get(section, {})
                if field in submitted and submitted[field] != normalized[identity]:
                    prefix = '/metadata_ref' if 'metadata_ref' in body else '/metadata'
                    fail(field=prefix + '/' + section + '/' + field,
                         reason='explicit metadata identity conflicts with request')
            if reserve_only:
                self.plans[key] = {'normalized': normalized, 'semantic': semantic}
                return {'status': 202, 'state': 'accepted'}
            if 'metadata_ref' in body:
                self.adopt_object(object_upload_id, expected_target, key, authority)
            return self.publish(normalized, semantic)
        return self.request(key, '/imports', semantic, effect, authority)

    def publish(self, normalized, semantic):
        doc, version, base = (normalized[k] for k in ('document_id', 'version_id', 'base_version_id'))
        if self.latest.get(doc) != base:
            fail('VERSION_CONFLICT')
        immutable = {'payload_sha256': semantic['payload_sha256'], 'metadata': semantic.get('metadata'), 'document_id': doc, 'title_snapshot': normalized['title_snapshot']}
        if version in self.versions:
            if self.versions[version] != immutable:
                fail('IMMUTABLE_VERSION_CONFLICT')
            return {'status': 200, 'version_id': version, 'document_id': doc}
        self.versions[version] = immutable
        self.latest[doc] = version
        self.document_titles[doc] = normalized['title_snapshot']
        return {'status': 201, 'version_id': version, 'document_id': doc}

    def summary(self, upload_id, detail=False, authorized=True):
        upload = self.uploads[upload_id]
        value = {'upload_id': upload_id, 'originating_principal_id': '11111111-1111-4111-8111-111111111111', 'originating_device_id': None, 'purpose': upload['purpose'], 'input_mode': upload['input_mode'], 'selection_name': upload['selection_name'], 'target': deepcopy(upload['target']), 'state': upload['state'], 'byte_size': str(len(upload['bytes'])), 'staging_bytes': str(len(upload['bytes'])), 'created_at': '2026-10-16T00:00:00Z', 'updated_at': '2026-10-16T00:00:00Z', 'reusable': self.reusable(upload_id, authorized), 'unavailable_reason': None if self.reusable(upload_id, authorized) else 'authority_or_bytes_unavailable', 'pin_summary': deepcopy(upload['pins']), 'pins_url': '/uploads/' + upload_id + '/pins', 'detail_url': '/uploads/' + upload_id}
        value['revision'] = upload['revision']
        value['etag'] = '"upload-' + upload_id + '-' + digest(json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8')) + '"'
        if detail:
            object_ref = self.object_reference(upload_id) if self.reusable(upload_id, authorized) and 'object_id' in upload else None
            value.update({'checkpoint': upload['checkpoint'], 'ranges_url': '/uploads/' + upload_id + '/ranges', 'entries_url': '/uploads/' + upload_id + '/entries', 'error': None, 'seal_job_id': upload['job'], 'seal_operation_id': None, 'sealed_content_set_id': upload.get('content_set_id'), 'sealed_object_ref': object_ref})
        validate('upload_detail' if detail else 'upload_summary', value)
        return value

    def commit_config(self, plan_id, expected_revision, changes, confirmed, alias=None, authority='owner', expired=False):
        self.authorize(authority, 'owner')
        body = {'expected_revision': expected_revision, 'changes': changes, 'confirmed': confirmed}
        key = alias or plan_id
        for identity in (plan_id, key):
            if identity in self.receipts and self.receipts[identity].get('plan_id') != plan_id:
                fail('OPERATION_CONFLICT')
        if plan_id in self.plans:
            old = self.plans[plan_id]
            if old['body'] != body:
                fail('OPERATION_CONFLICT')
            for identity in (plan_id, key):
                self.receipts[identity] = {'plan_id': plan_id}
            return old['result']
        if expired:
            fail('STALE_PLAN')
        if not confirmed:
            fail(field='/confirmed', reason='impact confirmation required')
        if expected_revision != self.config_revision:
            fail('PRECONDITION_FAILED')
        policy = configuration(merge_configuration(self.policy, changes))
        access_changed = access_projection(policy) != access_projection(self.policy)
        self.policy = policy
        self.config_revision += 1
        self.permissions_revision += int(access_changed)
        result = {'config_revision': self.config_revision, 'permissions_revision': self.permissions_revision}
        self.plans[plan_id] = {'body': deepcopy(body), 'result': result}
        for identity in (plan_id, key):
            self.receipts[identity] = {'plan_id': plan_id}
        return result
