export namespace main {
	
	export class FileTransfer {
	    id: string;
	    filename: string;
	    size: number;
	    progress: number;
	    status: string;
	    code?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileTransfer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.filename = source["filename"];
	        this.size = source["size"];
	        this.progress = source["progress"];
	        this.status = source["status"];
	        this.code = source["code"];
	    }
	}

}

