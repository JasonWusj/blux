import type {Task} from './types';
export interface Collaboration{id:string;title:string;goal:string;repository_path:string;context_ids:string[];created_at:string;updated_at:string;status:string;artifact_count:number;tasks:Task[];pinned_at?:string}
export interface SharedArtifact{id:string;snapshot_id:string;task_id:string;turn_id:string;path:string;hash:string;size:number;mime:string;created_at:string}
export interface SharedSnapshot{id:string;task_id:string;task_title:string;turn_id:string;generation:number;adapter:string;commit:string;diff:string;created_at:string;artifacts:SharedArtifact[]}
export const statusLabels:Record<string,string>={pending:'待开始',queued:'排队中',running:'进行中',completed:'已完成',failed:'未完成',attention:'需要处理'};
