import {spawnSync} from 'node:child_process';
import {resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const result=spawnSync('go',['build','-o',resolve(root,process.platform==='win32'?'bin/blux.exe':'bin/blux'),'./cmd/blux'],{cwd:root,stdio:'inherit'});
if(result.error)console.error(result.error.message);
process.exit(result.status??1);
