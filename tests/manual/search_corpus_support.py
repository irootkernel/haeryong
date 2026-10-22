"""Shared public-import/byte/job preparation for explicitly selected SC19 clients."""
import hashlib
from pathlib import Path
import time
import subprocess
import tempfile
import urllib.parse

REPO = Path(__file__).resolve().parents[2]
EXAMPLES = [
    {'name':'mulgae','q':'failed_run_recovery','page':'recovery/inventory.html','region':'M01','heading':'Recover roles from a proven source'},
    {'name':'gaori','q':'"accepted cancellation"','page':'gaori/lifecycle.html','region':'G10','heading':'A finished invocation is terminal; a waiter ending is not'},
]


def prepare(product, owner):
    output=[]
    for example in EXAMPLES:
        name=example['name']
        project=product.cli(['projects','register','--name','SC19 '+name],client=owner)['data']['result']['project_id']
        original_root=REPO/'spikes/knowledge-value/samples'/name
        root=Path(tempfile.mkdtemp(prefix='sc19-source-',dir=product.root))
        selected=subprocess.check_output(['git','ls-files','-z','--','spikes/knowledge-value/samples/'+name],cwd=REPO).decode().split('\0')
        for path in selected:
            if not path:continue
            source=REPO/path;target=root/source.relative_to(original_root);target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(source.read_bytes())
        _,_,published=product.import_tree(owner,root,extra=['--project',project],timeout=120)
        version=published['version_id']
        expected={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(root.rglob('*')) if p.is_file()}
        paths=[row['path'] for row in product.files(owner,version) if row['kind']=='file']
        assert sorted(paths)==sorted(expected)
        for path,digest in expected.items():
            status,body,_=product.request('GET',f'/api/v1/versions/{version}/file?'+urllib.parse.urlencode({'path':path}),client=owner,raw=True)
            assert status==200 and hashlib.sha256(body).hexdigest()==digest,(name,path,'byte identity')
        deadline=time.monotonic()+120
        while True:
            jobs=product.data('GET','/api/v1/jobs',client=owner)['items']
            derived=[j for j in jobs if j['kind']=='derived_index' and j.get('result',{}).get('version_id')==version]
            if derived:
                assert len(derived)==1
                job=product.data('GET','/api/v1/jobs/'+derived[0]['job_id'],client=owner)
                assert job['state'] not in ('failed','cancelled','interrupted')
                if job['state']=='completed':
                    assert job['result']['stage']=='complete' and job['result']['failed']=='0'
                    break
            assert time.monotonic()<deadline,'sample job completion deadline'
            time.sleep(.1)
        output.append(example|{'project':project,'document':published['document_id'],'version':version,'tree':expected})
    return output
