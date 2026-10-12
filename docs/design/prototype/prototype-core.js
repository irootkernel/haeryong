(function(){
  'use strict';
  const seedArticle=document.createElement('template');
  seedArticle.innerHTML=HR.fragments.reader||'';
  const initialArticle=seedArticle.content.querySelector('#article');
  const intro=initialArticle?initialArticle.innerHTML:'<h2>현재 소유권을 확인한 뒤 결과를 반영합니다</h2><p>재시도가 시작되면 이전 실행이 늦게 돌아올 수 있습니다. 결과를 반영하기 직전에 현재 소유자와 결과의 소유자가 같은지 확인합니다.</p>';
  const p=(title,path,html)=>({title,path,html});
  const pages={
    index:p('재시도 중 작업 소유권이 바뀌면','docs/실행기/재시도/index.html',intro),
    flow:p('이전 실행의 결과를 거절하는 순서','docs/실행기/재시도/흐름.html','<p class="eyebrow">실행 흐름 · 02</p><h2>늦게 도착한 성공도 확인이 필요합니다</h2><p>첫 실행이 응답하지 않으면 다음 실행이 작업을 이어받습니다. 이때 소유권의 세대가 바뀌므로 이전 실행이 들고 있는 값은 더 이상 현재 값이 아닙니다.</p><ol><li>새 실행이 현재 소유권을 확인합니다.</li><li>결과가 들고 온 소유권과 비교합니다.</li><li>두 값이 같을 때만 저장하고, 다르면 결과를 거절합니다.</li></ol><div class="notice">판단 시점은 결과를 받았을 때입니다. 처음 실행을 시작한 시점의 확인만으로는 충분하지 않습니다.</div><h2>실패와 오래된 결과를 구분합니다</h2><p>실행 실패는 다시 시도할 수 있습니다. 오래된 소유권의 결과는 저장하지 않는 것이 정상 동작입니다.</p><pre class="code">claimedOwner === currentOwner\n  ? commit(result)\n  : rejectStaleResult(result)</pre>'),
    evidence:p('설명에 사용한 근거와 확인 범위','docs/실행기/재시도/근거.html','<p class="eyebrow">근거 읽기 · 03</p><h2>어디까지 확인한 설명인가요?</h2><p>이 시안의 원본과 리비전은 디자인 검토용 샘플입니다. 본문 작성 시 사용한 근거와 이후 확인 기록을 구분해 표시합니다.</p><div class="table-wrap"><table><thead><tr><th>대상</th><th>확인 범위</th><th>상태</th></tr></thead><tbody><tr><td>소유권 비교</td><td>실행기 코드</td><td>일치</td></tr><tr><td>재시도 순서</td><td>절차 문서</td><td>일치</td></tr><tr><td>네트워크 단절</td><td>운영 시나리오</td><td>미확인</td></tr></tbody></table></div><p>일부 영역을 확인하지 못했다는 사실은 그대로 남깁니다. 개인이 문서를 읽고 검토한 상태와 원본의 최신성은 별개입니다.</p>')
  };
  const clonePages=()=>JSON.parse(JSON.stringify(pages));
  const versions=()=>({v1:{id:'v1',date:'2026-10-08 09:10',review:'reviewed',pages:clonePages()},v2:{id:'v2',date:'2026-10-10 14:30',review:'reviewed',pages:clonePages()},v3:{id:'v3',date:'2026-10-12 08:40',review:'reviewing',pages:clonePages()}});
  const doc=(id,project,title,path,freshness,lifecycle='active')=>({id,project,title,path,freshness,source:freshness,lifecycle,priorLifecycle:'active',preferred:'v3',last:{version:'v3',page:'index'},versions:versions()});
  HR.docs=[
    doc('podway','podway','재시도 중 작업 소유권이 바뀌면','Podway/실행기/재시도', 'changed'),
    doc('haeryong','haeryong','문서 버전과 개인 검토 기록은 어떻게 이어질까','Haeryong/설계/문서 버전과 개인 검토','current'),
    doc('guide','haeryong','연결이 끊긴 상태에서도 작성 중인 질문과 메모를 안전하게 이어가는 방법','Haeryong/사용 안내/연결 복구/질문과 메모','current'),
    doc('unknown','podway','팀에서 받은 HTML 설명 모음','받은 문서/설명 모음/시작하기','unknown'),
    doc('archived','haeryong','초기 연결 프로토콜','Haeryong/보관/초기 연결','current','archived'),
    doc('deleted1','podway','예전 실행기 용어집','Podway/용어/예전 용어집','unknown','trashed'),
    doc('deleted2','haeryong','이전 설정 화면 검토','Haeryong/보관/설정 검토','current','trashed')
  ];
  HR.docs.find(d=>d.id==='deleted2').priorLifecycle='archived';
  const contentById={
    haeryong:['문서와 검토 기록을 함께 읽습니다','새 버전이 추가되어도 이전 버전의 검토 기록과 메모는 남아 있습니다. 새 버전을 여는 동작과 그 버전 전체를 검토 완료로 표시하는 동작을 나누어, 읽었다는 사실이 과장되지 않게 합니다.','기본으로 열 버전은 새로 들어갈 때의 기준입니다. 이어서 읽기는 마지막으로 읽은 버전과 페이지를 그대로 사용합니다.'],
    guide:['연결이 끊겨도 작성 중인 내용은 유지합니다','질문을 쓰기 시작한 페이지를 함께 기억합니다. 다른 페이지로 이동해도 작성 중인 질문의 대상은 저절로 바뀌지 않습니다. 질문 대상을 바꾸려면 현재 페이지로 변경을 선택합니다.','메모는 문서, 버전, 페이지에 연결합니다. 저장하지 않은 초안은 탭이나 패널을 닫았다가 돌아와도 확인할 수 있습니다. 이 프로토타입의 상태는 새로고침하면 초기화됩니다.'],
    unknown:['원본을 모르는 설명도 읽을 수 있습니다','가져온 HTML에 원본 정보가 없어도 본문은 열립니다. 근거 영역에서는 원본 정보 없음으로 표시하고, 검증되었다는 표시를 붙이지 않습니다.','질문은 저장된 설명을 기준으로 시작할 수 있습니다. 이후 프로젝트의 원본을 연결하면 질문에 사용할 범위를 선택할 수 있습니다.'],
    archived:['이전 설계를 참고할 때','현재 목록에서 보관한 문서입니다. 보관은 삭제와 다르며 읽기와 기록 확인이 가능합니다. 필요해지면 보관 해제하여 서재로 돌려보냅니다.','휴지통으로 보낸 경우에는 이전 상태를 기억하여 복원합니다. 보관함에서 삭제했다면 보관함으로 복원됩니다.']
  };
  for(const d of HR.docs){
    for(const v of Object.values(d.versions)){
      if(contentById[d.id]){const [h,a,b]=contentById[d.id];v.pages.index={title:d.title,path:d.path+'/index.html',html:'<p class="eyebrow">설계 노트</p><h2>'+h+'</h2><p>'+a+'</p><h2>사용자의 다음 행동</h2><p>'+b+'</p><div class="notice">저장된 설명 · 샘플 문서</div>'};v.pages.flow={title:'사용 흐름',path:d.path+'/흐름.html',html:'<h2>한 번에 하나의 결정을 합니다</h2><p>'+b+'</p>'};v.pages.evidence={title:'근거와 범위',path:d.path+'/근거.html',html:pages.evidence.html};}
      if(v.id==='v1')v.review='unreviewed';
      if(d.id==='haeryong'||d.id==='guide')v.review='reviewed';
      if(d.id==='unknown')v.review='unreviewed';
    }
  }
  HR.docs[0].versions.v2.pages.index.html='<h2>저장 단계의 이전 설명</h2><p>응답이 도착하면 결과를 저장하고 작업을 완료합니다. 재시도로 실행 주체가 바뀌는 경우의 소유권 확인은 이 버전에 아직 설명하지 않았습니다.</p><pre class="code">save(result)</pre>';
  HR.docs[0].versions.v1.pages.index.html='<h2>첫 실행 흐름</h2><p>작업을 시작하고 결과가 돌아오면 완료로 기록합니다. 이 버전에는 재시도 흐름이 포함되어 있지 않습니다.</p>';
  HR.state={route:'reader',docId:'podway',versionId:'v3',pageId:'index',context:null,scenario:'normal',theme:'light',projectId:'podway',report:'coverage',setting:'general',noteDrafts:{},savedNotes:{},questionDraft:{text:'',captured:null,quote:'',mode:'snapshot',answers:[]},revisit:['guide'],filters:{search:'',project:'all',review:'all',freshness:'all'},finding:{decision:'pending',reassessed:false},draft:{status:'ready',base:'v3',title:'재시도 결과를 확인하는 기준'},scan:{status:'idle',progress:0},upload:{status:'idle',progress:0,kind:'new',title:'연결 복구 안내',base:'v3',docId:'podway',sample:'folder',resultId:null},pair:{status:'idle',remember:true},settings:{fontSize:'16',lineHeight:'1.7',retentionDays:'30',autoPurge:false},backup:{completed:'2026-10-11 18:20',attempt:'성공',trial:'미실행'},devices:[{id:'mac',name:'이 Mac',current:true},{id:'phone',name:'iPhone',current:false}]};
  HR.$=id=>document.getElementById(id);
  HR.escape=x=>String(x==null?'':x).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  HR.doc=id=>HR.docs.find(d=>d.id===(id||HR.state.docId));
  HR.version=()=>HR.doc()?.versions?.[HR.state.versionId];
  HR.target=()=>{const d=HR.doc(),v=HR.version(),p=v?.pages?.[HR.state.pageId];return d&&v&&p?{docId:d.id,versionId:v.id,pageId:HR.state.pageId,title:d.title,path:p.path}:{docId:null,versionId:null,pageId:null,title:'선택한 문서 없음',path:''};};
  HR.targetKey=t=>{t=t||HR.target();return[t.docId,t.versionId,t.pageId].join('|');};
  let toastTimer,dialogOpener=null;
  HR.toast=t=>{const e=HR.$('toast');e.textContent=t;e.hidden=false;clearTimeout(toastTimer);toastTimer=setTimeout(()=>{e.hidden=true;},2600);};
  HR.openDialog=(title,body,footer)=>{const d=HR.$('appDialog');if(!d.open)dialogOpener=document.activeElement;HR.$('dialogTitle').textContent=title;HR.$('dialogBody').innerHTML=body;HR.$('dialogFooter').innerHTML=footer||'<button class="button" data-action="dialog:close" autofocus>닫기</button>';if(!d.open)d.showModal();const initial=d.querySelector('[autofocus]')||d.querySelector('button,input,select,textarea');if(initial)initial.focus();};
  HR.closeDialog=()=>{const d=HR.$('appDialog');if(d.open)d.close();if(dialogOpener&&dialogOpener.isConnected)dialogOpener.focus();};
  HR.newVersion=(d,html,title)=>{d.versions=d.versions||{};const n=Math.max(0,...Object.keys(d.versions).map(x=>Number(x.slice(1))||0))+1,id='v'+n;const base=d.versions[d.last?.version]||Object.values(d.versions).slice(-1)[0];const ps=base?JSON.parse(JSON.stringify(base.pages)):{index:{title:title||d.title,path:d.path+'/index.html',html:''}};if(html)ps.index.html=html;if(title)ps.index.title=title;const v={id,date:'2026-10-12 11:00',review:'unreviewed',pages:ps};d.versions[id]=v;if(!d.preferred)d.preferred=id;if(!d.last)d.last={version:id,page:'index'};return v;};
  HR.go=route=>{const d=HR.doc();if(['reader','versions'].includes(route)&&(!d||['purged','trashed'].includes(d.lifecycle)||!Object.keys(d.versions).length)){HR.toast('선택한 문서를 현재 열 수 없습니다. 서재에서 다른 문서를 선택하세요.');route='library';}HR.state.route=route;if(route!=='reader')HR.state.context=null;HR.closeNav();HR.render();};
  HR.openDoc=(id,version,page)=>{const d=HR.doc(id);if(!d||d.lifecycle==='purged'||d.lifecycle==='trashed'){HR.toast('이 문서는 현재 열 수 없습니다. 휴지통에서 상태를 확인하세요.');return;}HR.state.docId=id;HR.state.versionId=version||d.preferred;HR.state.pageId=page||'index';if(!d.versions[HR.state.versionId])HR.state.versionId=d.preferred;if(!HR.version().pages[HR.state.pageId])HR.state.pageId=Object.keys(HR.version().pages)[0];d.last={version:HR.state.versionId,page:HR.state.pageId};HR.go('reader');};
  HR.closeNav=()=>{const nav=document.querySelector('.sidebar');if(nav)nav.classList.remove('open');const cover=HR.$('navBackdrop');if(cover)cover.hidden=true;};
  const reviewNames={unreviewed:'미검토',reviewing:'검토 중',reviewed:'검토 완료'};
  const freshNames={current:'원본과 일치',changed:'원본 변경됨',unknown:'원본 정보 없음'};
  HR.renderRows=docs=>docs.map(d=>{const v=d.versions[d.preferred]||Object.values(d.versions).slice(-1)[0];return'<article class="list-row"><div class="row-main"><button class="row-title button ghost" data-action="open:'+HR.escape(d.id)+'">'+HR.escape(d.title)+'</button><div class="row-meta">'+HR.escape(d.project==='podway'?'Podway':'Haeryong')+' · '+v.id+' · '+HR.escape(d.path)+'</div></div><div class="row-end"><span class="chip">내 검토: '+reviewNames[v.review]+'</span><span class="small '+(d.freshness==='changed'?'warn':'muted')+'">'+freshNames[d.freshness]+'</span></div></article>';}).join('');
  HR.renderLibrary=()=>{const s=HR.state,f=s.filters;const el=HR.$('libraryRows');if(!el)return;const result=HR.docs.filter(d=>d.lifecycle==='active'&&(!f.search||(d.title+' '+d.path+' '+d.project).toLowerCase().includes(f.search.toLowerCase()))&&(f.project==='all'||d.project===f.project)&&(f.freshness==='all'||d.freshness===f.freshness)&&(f.review==='all'||d.versions[d.preferred].review===f.review));el.innerHTML=HR.renderRows(result);const empty=HR.$('libraryEmpty');if(empty)empty.hidden=result.length>0;};
  HR.renderLifecycle=()=>{for(const [id,state]of[['archiveRows','archived'],['trashRows','trashed']]){const el=HR.$(id);if(!el)continue;const docs=HR.docs.filter(d=>d.lifecycle===state);el.innerHTML=docs.length?docs.map(d=>'<article class="list-row"><div><h2 class="row-title">'+HR.escape(d.title)+'</h2><p class="row-meta">'+HR.escape(d.path)+' · '+(state==='archived'?'보관 중':(d.priorLifecycle==='archived'?'보관함':'서재')+'로 복원')+'</p></div><div class="row"><button class="button" data-action="'+(state==='archived'?'open:':'lifecycle:restore:')+d.id+'">'+(state==='archived'?'읽기':'복원')+'</button><button class="button '+(state==='trashed'?'danger':'')+'" data-action="'+(state==='archived'?'lifecycle:unarchive:':'dialog:purge:')+d.id+'">'+(state==='archived'?'보관 해제':'영구 삭제')+'</button></div></article>').join(''):'<div class="empty"><h2>'+(state==='archived'?'보관한 문서가 없습니다':'휴지통이 비어 있습니다')+'</h2><p>문서 상태를 바꾸면 이 목록에 반영됩니다.</p><button class="button" data-action="go:library">서재 열기</button></div>';}};
  const sectionKeys={projects:'projectSettings',maintenance:'maintenanceSettings'};
  const labels={home:'홈',library:'서재',projects:'프로젝트',sources:'원본',reader:'읽기',versions:'버전 비교',maintenance:'정비',archive:'보관함',trash:'휴지통',settings:'설정',connect:'연결'};
  HR.renderContext=()=>{const e=HR.$('context');if(!e)return;const selected=HR.state.context;e.hidden=!selected||HR.state.route!=='reader';document.querySelector('.app').classList.toggle('has-context',!e.hidden);if(e.hidden)return;const tabs=[['ask','질문'],['evidence','근거'],['notes','메모'],['links','연결'],['files','파일']];e.innerHTML='<header class="context-head"><span class="eyebrow">읽기 도구</span><button class="button ghost" data-action="context:close" aria-label="읽기 도구 닫기">닫기</button></header><div class="tabs context-tabs" aria-label="읽기 도구 종류">'+tabs.map(([key,title])=>'<button class="tab button '+(selected===key?'active':'')+'" aria-pressed="'+(selected===key)+'" data-action="context:'+key+'">'+title+'</button>').join('')+'</div><div class="context-body">'+(HR.renderReaderContext?HR.renderReaderContext():HR.fragments[selected]||'')+'</div>';if(HR.afterReaderRender)HR.afterReaderRender();};
  HR.renderBanner=()=>{const e=HR.$('banner'),s=HR.state.scenario;const map={disconnected:['연결이 끊겼습니다','열어 둔 설명과 작성 중인 초안은 유지됩니다. 연결 상태를 확인해 주세요.'],source:['원본을 열 수 없습니다','저장된 설명은 계속 읽을 수 있습니다. 근거 범위는 확인 가능한 정보까지만 표시합니다.'],provider:['질문 제공자를 사용할 수 없습니다','읽기와 메모는 계속 사용할 수 있습니다. 질문 초안은 그대로 남습니다.'],conflict:['메모에 다른 변경이 있습니다','내 초안과 저장된 내용을 비교한 뒤 선택할 수 있습니다.'],partial:['일부 범위만 확인했습니다','구조 확인과 의미 확인의 범위를 나누어 표시합니다.'],stale:['초안의 기준 버전이 달라졌습니다','새 기준으로 초안을 다시 준비한 후 게시해 주세요.'],session:['세션이 만료되었습니다','작성 중인 내용은 유지됩니다. 다시 연결하여 이어갈 수 있습니다.']};if(!map[s]){e.innerHTML='';return;}const [title,body]=map[s];e.innerHTML='<div class="notice scenario-notice"><div><strong>'+title+'</strong><span>'+body+'</span></div><button class="button ghost" data-action="'+(s==='session'?'go:connect':s==='disconnected'?'go:settings':'scenario:normal')+'">'+(s==='session'?'다시 연결':s==='disconnected'?'연결 설정':'일반 상태로')+'</button></div>';};
  HR.render=()=>{
    const s=HR.state,screen=HR.$('screen');screen.innerHTML=HR.fragments[s.route]||'<h1>'+HR.escape(labels[s.route]||s.route)+'</h1>';
    const crumb=HR.$('breadcrumb');if(crumb)crumb.textContent=s.route==='reader'?HR.doc().project+' / '+HR.state.versionId+' / '+HR.version().pages[s.pageId].path:labels[s.route];
    document.querySelectorAll('.sidebar [data-action^="go:"]').forEach(b=>{const navRoute=['reader','versions'].includes(s.route)?'library':s.route==='sources'?'projects':s.route;const active=b.dataset.action==='go:'+navRoute;b.classList.toggle('active',active);if(active)b.setAttribute('aria-current','page');else b.removeAttribute('aria-current');});
    const main=HR.$('main');if(main)main.dataset.route=s.route;
    if(s.route==='library'){for(const [id,value]of[['librarySearch',s.filters.search],['projectFilter',s.filters.project],['reviewFilter',s.filters.review],['freshnessFilter',s.filters.freshness]]){if(HR.$(id))HR.$(id).value=value;}HR.renderLibrary();}
    if(s.route==='projects'){const rows=HR.$('projectRows');if(rows)rows.innerHTML=HR.renderRows(HR.docs.filter(d=>d.lifecycle==='active'&&d.project===s.projectId));const h=screen.querySelector('h1');if(h)h.textContent='프로젝트';const sub=screen.querySelector('.page-header .muted');if(sub)sub.textContent='프로젝트별 문서와 연결된 원본을 함께 확인합니다.';for(const card of screen.querySelectorAll('.card')){const pick=card.querySelector('[data-action^="project:"]');if(!pick)continue;const project=pick.dataset.action.split(':')[1];const source=card.querySelector('[data-action="go:sources"]');if(source)source.dataset.action='sources:'+project;const paragraphs=card.querySelectorAll('p');for(const p of paragraphs)if(!p.className){const count=HR.docs.filter(d=>d.project===project&&d.lifecycle==='active').length;p.textContent='저장된 문서 '+count+'개 · 원본 범위와 질문 기본값을 따로 관리합니다.';}}}
    if(s.route==='sources'){if(HR.$('sourceProjectTitle'))HR.$('sourceProjectTitle').textContent=(s.projectId==='podway'?'Podway':'Haeryong')+' 원본';if(HR.$('sourcePath'))HR.$('sourcePath').textContent='./'+s.projectId;const key=s.projectId==='podway'?'projectSourcePodway':'projectSourceHaeryong';if(HR.$('sourceMode'))HR.$('sourceMode').value=s.settings[key]&&s.settings[key]!=='inherit'?s.settings[key]:s.settings.defaultSource||'snapshot';}
    if(s.route==='archive'||s.route==='trash')HR.renderLifecycle();
    if(s.route==='maintenance'&&HR.$('reportBody'))HR.$('reportBody').innerHTML=HR.fragments[s.report];
    if(s.route==='settings'&&HR.$('settingsBody'))HR.$('settingsBody').innerHTML=HR.fragments[sectionKeys[s.setting]||s.setting];
    HR.renderBanner();HR.renderContext();
    for(const fn of['afterReaderRender','afterManagementRender','afterSettingsRender'])if(HR[fn])HR[fn]();
    if(s.route==='home')HR.syncHome();
    if(s.route==='versions')HR.syncVersions();
  };
  HR.syncHome=()=>{const node=HR.$('screen'),d=HR.docs.find(x=>x.id===HR.state.docId&&['active','archived'].includes(x.lifecycle))||HR.docs.find(x=>x.lifecycle==='active');const title=node.querySelector('.page-header h1');if(title){title.textContent='이어서 읽어 볼까요?';title.classList.remove('small');}const sub=node.querySelector('.page-header .muted');if(sub)sub.textContent='읽던 문서와 다시 살펴볼 내용을 모았습니다.';const feature=node.querySelector('.reading');if(feature&&!d)feature.innerHTML='<p class="eyebrow">처음 시작하기</p><h2>읽을 문서를 가져와 보세요</h2><p>HTML, 폴더, ZIP 샘플로 서재를 시작할 수 있습니다.</p><button class="button primary" data-action="dialog:import">가져오기</button>';if(feature&&d){feature.innerHTML='<p class="eyebrow">이어서 읽기</p><h2>'+HR.escape(d.title)+'</h2><p class="row-meta">'+HR.escape(d.project)+' · '+d.last.version+' · '+HR.escape(d.versions[d.last.version].pages[d.last.page].title)+'</p><p class="muted">마지막으로 읽은 버전과 페이지에서 이어집니다.</p><button class="button primary" data-action="continue:'+d.id+'">이어서 읽기</button>';}
    const lists=node.querySelectorAll('.section .list');if(lists[0]){const changed=HR.docs.filter(d=>d.lifecycle==='active'&&d.versions[d.preferred].review!=='reviewed'&&Object.values(d.versions).some(v=>v.review==='reviewed'));lists[0].innerHTML=changed.length?HR.renderRows(changed):'<p class="empty">검토한 이후 추가된 버전이 없습니다.</p>';}
    if(lists[1]){const revisit=HR.docs.filter(d=>HR.state.revisit.includes(d.id)&&d.lifecycle==='active');lists[1].innerHTML=revisit.length?HR.renderRows(revisit):'<p class="empty">나중에 읽기로 표시한 문서가 없습니다.</p>';}
    const retained=HR.state.upload.status!=='idle'&&!['complete','completed','abandoned'].includes(HR.state.upload.status);if(retained){const n=document.createElement('div');n.className='notice';n.innerHTML='<strong>이어서 가져올 파일이 있습니다</strong><p>'+HR.escape(HR.state.upload.title)+' · '+HR.state.upload.progress+'%</p><button class="button" data-action="dialog:import">진행 상황 보기</button>';node.prepend(n);}};
  HR.syncVersions=()=>{
    const s=HR.state,d=HR.doc(),ids=Object.keys(d.versions).sort((a,b)=>Number(a.slice(1))-Number(b.slice(1)));
    const old=HR.$('compareOld'),next=HR.$('compareNew'),pref=HR.$('preferredVersion');if(!old||!next||!ids.length)return;
    s.compareOld=d.versions[s.compareOld]?s.compareOld:ids[Math.max(0,ids.length-2)];
    s.compareNew=d.versions[s.compareNew]?s.compareNew:ids[ids.length-1];
    const options=ids.map(id=>'<option value="'+id+'">'+id+' · '+d.versions[id].date+'</option>').join('');old.innerHTML=options;next.innerHTML=options;old.value=s.compareOld;next.value=s.compareNew;if(pref){pref.innerHTML=options;pref.value=d.preferred;}
    const heading=HR.$('screen').querySelector('h1');if(heading)heading.textContent=d.title;
    const summary=HR.$('screen').querySelector('.page-header .muted');if(summary)summary.textContent=ids.length>1?s.compareOld+'와 '+s.compareNew+'의 저장된 설명을 비교합니다.':'아직 다른 버전이 없습니다. 현재 버전의 내용을 확인할 수 있습니다.';
    const left=d.versions[s.compareOld],right=d.versions[s.compareNew];
    const extract=version=>{const first=version.pages.index||Object.values(version.pages)[0],node=document.createElement('div');node.innerHTML=first.html||'';return Array.from(node.querySelectorAll('p')).map(p=>p.textContent).join(' ').slice(0,650)||node.textContent.slice(0,650);};
    const leftText=extract(left),rightText=extract(right),same=leftText===rightText;
    const body=HR.$('diffBody');if(body)body.innerHTML='<div class="grid diff-grid"><section class="diff-old"><p class="eyebrow">'+s.compareOld+'</p><h2>비교 기준</h2><p>'+HR.escape(leftText)+'</p></section><section class="diff-new"><p class="eyebrow">'+s.compareNew+'</p><h2>'+(same?'본문이 같습니다':'비교 대상')+'</h2><p>'+HR.escape(rightText)+'</p></section></div>';
    const table=HR.$('screen').querySelector('.table-wrap tbody');if(table){const paths=Array.from(new Set([...Object.keys(left.pages),...Object.keys(right.pages)]));table.innerHTML=paths.map(id=>{const a=left.pages[id],b=right.pages[id],label=!a?'추가됨':!b?'삭제됨':a.html===b.html?'변경 없음':'본문 변경';return '<tr><td>'+HR.escape((b||a).path)+'</td><td>'+s.compareOld+'</td><td>'+label+'</td></tr>';}).join('');}
    for(const button of HR.$('screen').querySelectorAll('[data-action="dialog:reattach"]')){const section=button.closest('.section');if(section)section.hidden=d.id!=='podway'||ids.length<2;}
  };
  HR.lifecycleAction=(action)=>{
    const parts=action.split(':'),kind=parts[1],id=parts[2]||HR.state.docId,d=HR.doc(id);if(!d)return false;
    if(action.startsWith('dialog:purge:')){HR.openDialog('영구 삭제 영향 확인','<div class="notice"><strong>'+HR.escape(d.title)+'</strong><p>샘플 문서의 모든 버전과 연결된 메모가 이 시안에서 제거됩니다. 다른 문서의 참조는 대상 없음으로 남습니다.</p><p>백업 샘플에는 영향을 주지 않습니다.</p></div>','<button class="button" data-action="dialog:close" autofocus>취소</button><button class="button danger" data-action="lifecycle:purge:'+id+'">샘플 문서 영구 삭제</button>');return true;}
    if(parts[0]!=='lifecycle')return false;
    if(kind==='archive'){d.lifecycle='archived';HR.closeDialog();HR.go('archive');}
    else if(kind==='trash'){d.priorLifecycle=d.lifecycle==='archived'?'archived':'active';d.lifecycle='trashed';HR.closeDialog();HR.go('trash');}
    else if(kind==='unarchive'){d.lifecycle='active';HR.render();HR.toast('서재로 이동했습니다.');}
    else if(kind==='restore'){d.lifecycle=d.priorLifecycle||'active';HR.render();HR.toast(d.lifecycle==='archived'?'보관함으로 복원했습니다.':'서재로 복원했습니다.');}
    else if(kind==='purge'){if(d.lifecycle!=='trashed'){HR.toast('문서 상태가 바뀌었습니다. 목록을 다시 확인하세요.');HR.closeDialog();HR.render();return true;}d.lifecycle='purged';d.versions={};const q=HR.state.questionDraft;q.answers=(q.answers||[]).filter(a=>!(a.targetKey||'').startsWith(id+'|'));if(q.captured&&q.captured.docId===id){q.text='';q.captured=null;q.quote='';}HR.state.revisit=HR.state.revisit.filter(x=>x!==id);if(HR.state.docId===id){const available=HR.docs.find(x=>x.lifecycle==='active')||HR.docs.find(x=>x.lifecycle==='archived');if(available){HR.state.docId=available.id;HR.state.versionId=available.last.version;HR.state.pageId=available.last.page;}}for(const k of Object.keys(HR.state.noteDrafts))if(k.startsWith(id+'|'))delete HR.state.noteDrafts[k];for(const k of Object.keys(HR.state.savedNotes))if(k.startsWith(id+'|'))delete HR.state.savedNotes[k];HR.closeDialog();HR.render();HR.toast('샘플 문서를 삭제했습니다.');}
    else return false;return true;
  };
  HR.coreAction=action=>{
    const [scope,value]=action.split(':');
    if(action==='dialog:close'){HR.closeDialog();return true;}
    if(scope==='go'){if(value==='settings'&&HR.state.route==='maintenance')HR.state.setting='maintenance';if(value==='settings'&&HR.state.scenario==='disconnected')HR.state.setting='connection';HR.go(value);return true;}
    if(scope==='open'){HR.openDoc(value);return true;}
    if(scope==='continue'){const d=HR.doc(value);if(d)HR.openDoc(value,d.last.version,d.last.page);return true;}
    if(action==='theme'){HR.state.theme=HR.state.theme==='light'?'dark':'light';HR.state.settings.themeChoice=HR.state.theme;document.documentElement.dataset.theme=HR.state.theme;if(HR.$('themeChoice'))HR.$('themeChoice').value=HR.state.theme;if(HR.afterSettingsRender)HR.afterSettingsRender();return true;}
    if(action==='nav:toggle'){const nav=document.querySelector('.sidebar');if(nav)nav.classList.toggle('open');HR.$('navBackdrop').hidden=!(nav&&nav.classList.contains('open'));return true;}
    if(action==='nav:close'){HR.closeNav();return true;}
    if(action==='library:reset'){HR.state.filters={search:'',project:'all',review:'all',freshness:'all'};HR.render();return true;}
    if(scope==='project'){HR.state.projectId=value;HR.go('projects');return true;}
    if(scope==='sources'){HR.state.projectId=value;HR.go('sources');return true;}
    if(action==='version:prefer'){HR.doc().preferred=HR.$('preferredVersion').value;HR.toast('새로 열 때 사용할 기본 버전을 변경했습니다. 이어서 읽는 위치는 유지됩니다.');return true;}
    if(action==='version:open'){HR.openDoc(HR.state.docId,HR.$('compareNew').value,'index');return true;}
    if(action==='source:apply'){const value=HR.$('sourceMode')?HR.$('sourceMode').value:'snapshot',label=value==='current'?'연결된 원본 함께 사용':'저장된 설명 기준';HR.state.pendingSourceMode=value;HR.openDialog('원본 사용 범위 변경','<p>이 프로젝트의 다음 질문부터 <strong>'+label+'</strong>을 기본으로 사용합니다. 저장된 설명과 기존 질문 기록은 유지됩니다.</p>','<button class="button" data-action="dialog:close" autofocus>취소</button><button class="button primary" data-action="source:confirm">변경 적용</button>');return true;}
    if(action==='source:confirm'){const key=HR.state.projectId==='podway'?'projectSourcePodway':'projectSourceHaeryong';HR.state.settings[key]=HR.state.pendingSourceMode;if(HR.state.settingsDraft)HR.state.settingsDraft[key]=HR.state.pendingSourceMode;HR.closeDialog();HR.toast('이 프로젝트의 다음 질문부터 적용합니다.');return true;}
    if(action==='dialog:scenario'){const options=[['normal','일반'],['disconnected','연결 끊김'],['source','원본 사용 불가'],['provider','질문 제공자 사용 불가'],['conflict','메모 충돌'],['partial','부분 확인'],['stale','오래된 초안'],['session','세션 만료']];HR.openDialog('시안 상태 미리보기','<p class="muted">제품의 예외 상황을 검토하는 샘플 상태입니다. 실제 연결이나 데이터를 변경하지 않습니다.</p><div class="grid scenario-grid">'+options.map(([k,t])=>'<button class="button '+(HR.state.scenario===k?'primary':'')+'" data-action="scenario:'+k+'">'+t+'</button>').join('')+'</div><div class="divider"></div><h3>공통 구성 요소</h3><div class="row"><span class="chip good">검토 완료</span><span class="chip warn">원본 변경</span><button class="button primary">기본 동작</button><button class="button" disabled>사용 불가</button></div><p class="small muted">상태와 초안은 현재 탭의 메모리에 유지됩니다. 새로고침하면 초기화됩니다.</p>');return true;}
    if(scope==='scenario'){HR.state.scenario=value;HR.closeDialog();HR.render();return true;}
    if(action.startsWith('lifecycle:')||action.startsWith('dialog:purge:'))return HR.lifecycleAction(action);
    return false;
  };
  HR.start=()=>{
    document.addEventListener('click',e=>{const b=e.target.closest('[data-action]');if(!b||b.disabled)return;const action=b.dataset.action;for(const fn of['coreAction','readerAction','managementAction','settingsAction'])if(HR[fn]&&HR[fn](action,b)){return;}HR.toast('이 동작은 다음 디자인 검토에서 구체화할 항목입니다.');});
    document.addEventListener('submit',e=>e.preventDefault());
    document.addEventListener('input',e=>{const t=e.target,id=t.id;if(id==='librarySearch'){HR.state.filters.search=t.value;HR.renderLibrary();return;}for(const fn of['readerInput','managementInput','settingsInput'])if(HR[fn]&&HR[fn](id,t.type==='checkbox'?t.checked:t.value,t))return;});
    document.addEventListener('change',e=>{const t=e.target,id=t.id;const mapping={projectFilter:'project',reviewFilter:'review',freshnessFilter:'freshness'};if(mapping[id]){HR.state.filters[mapping[id]]=t.value;HR.renderLibrary();return;}if(id==='compareOld'||id==='compareNew'){HR.state[id]=t.value;HR.syncVersions();return;}for(const fn of['readerChange','managementChange','settingsChange'])if(HR[fn]&&HR[fn](id,t.type==='checkbox'?t.checked:t.value,t))return;});
    document.addEventListener('keydown',e=>{if(e.key==='Escape'){HR.closeNav();if(HR.$('appDialog').open)return;if(HR.state.context&&HR.readerAction)HR.readerAction('context:close');}if(e.key==='Tab'&&!HR.$('appDialog').open&&HR.state.context&&innerWidth<768){const panel=HR.$('context'),items=[...panel.querySelectorAll('button,input,select,textarea,a[href]')].filter(x=>!x.disabled&&x.getClientRects().length);if(!items.length)return;const first=items[0],last=items[items.length-1];if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}});
    HR.$('appDialog').addEventListener('cancel',()=>setTimeout(()=>{if(dialogOpener&&dialogOpener.isConnected)dialogOpener.focus();},0));
    HR.render();
  };
})();
