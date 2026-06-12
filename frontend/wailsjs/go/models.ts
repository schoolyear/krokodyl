export namespace main {
	
	export class FileTransfer {
	    id: string;
	    name: string;
	    files: string[];
	    size: number;
	    progress: number;
	    speed: number;
	    status: string;
	    code?: string;
	    peer?: string;
	    error?: string;
	    paths?: string[];
	    resendable?: boolean;
	    peerMachineId?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileTransfer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.files = source["files"];
	        this.size = source["size"];
	        this.progress = source["progress"];
	        this.speed = source["speed"];
	        this.status = source["status"];
	        this.code = source["code"];
	        this.peer = source["peer"];
	        this.error = source["error"];
	        this.paths = source["paths"];
	        this.resendable = source["resendable"];
	        this.peerMachineId = source["peerMachineId"];
	    }
	}
	export class NearbyPeer {
	    id: string;
	    name: string;
	    addr: string;
	    port: number;
	    machineId: string;
	
	    static createFrom(source: any = {}) {
	        return new NearbyPeer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.addr = source["addr"];
	        this.port = source["port"];
	        this.machineId = source["machineId"];
	    }
	}
	export class NearbyPrefs {
	    visible: boolean;
	    lastPeer: string;
	
	    static createFrom(source: any = {}) {
	        return new NearbyPrefs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.visible = source["visible"];
	        this.lastPeer = source["lastPeer"];
	    }
	}
	export class ResendOutcome {
	    started: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ResendOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.started = source["started"];
	        this.message = source["message"];
	    }
	}

}

