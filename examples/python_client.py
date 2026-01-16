import asyncio
import csv
import io
import json
import logging
import sys
import time
from dataclasses import dataclass, field
from typing import List, Optional, Dict, Any

# Try importing aiohttp
try:
    import aiohttp
except ImportError:
    aiohttp = None

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

@dataclass
class MapsResult:
    input_id: str
    link: str
    cid: str
    title: str
    category: str
    address: str
    open_hours: Dict[str, List[str]]
    popular_times: Dict[str, Dict[int, int]]
    web_site: str
    phone: str
    plus_code: str
    review_count: int
    review_rating: float
    reviews_per_rating: Dict[int, int]
    latitude: float
    longitude: float
    status: str
    description: str
    reviews_link: str
    thumbnail: str
    timezone: str
    price_range: str
    data_id: str
    place_id: str
    images: List[Dict[str, str]]
    reservations: List[Dict[str, str]]
    order_online: List[Dict[str, str]]
    menu: Dict[str, str]
    owner: Dict[str, str]
    complete_address: Dict[str, str]
    about: List[Dict[str, Any]]
    user_reviews: List[Dict[str, Any]]
    emails: List[str]

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> 'MapsResult':
        return cls(
            input_id=data.get('input_id', ''),
            link=data.get('link', ''),
            cid=data.get('cid', ''),
            title=data.get('title', ''),
            category=data.get('category', ''),
            address=data.get('address', ''),
            open_hours=data.get('open_hours', {}),
            popular_times=data.get('popular_times', {}),
            web_site=data.get('web_site', ''),
            phone=data.get('phone', ''),
            plus_code=data.get('plus_code', ''),
            review_count=data.get('review_count', 0),
            review_rating=data.get('review_rating', 0.0),
            reviews_per_rating=data.get('reviews_per_rating', {}),
            latitude=data.get('latitude', 0.0),
            longitude=data.get('longtitude', 0.0) if 'longtitude' in data else data.get('longitude', 0.0),
            status=data.get('status', ''),
            description=data.get('description', ''),
            reviews_link=data.get('reviews_link', ''),
            thumbnail=data.get('thumbnail', ''),
            timezone=data.get('timezone', ''),
            price_range=data.get('price_range', ''),
            data_id=data.get('data_id', ''),
            place_id=data.get('place_id', ''),
            images=data.get('images', []),
            reservations=data.get('reservations', []),
            order_online=data.get('order_online', []),
            menu=data.get('menu', {}),
            owner=data.get('owner', {}),
            complete_address=data.get('complete_address', {}),
            about=data.get('about', []),
            user_reviews=data.get('user_reviews', []),
            emails=data.get('emails', [])
        )

    @classmethod
    def from_csv_row(cls, row: Dict[str, str]) -> 'MapsResult':
        def parse_json(field_name: str, default=None):
            val = row.get(field_name, '')
            if not val:
                return default
            try:
                return json.loads(val)
            except json.JSONDecodeError:
                return default

        def parse_float(field_name: str, default=0.0):
            val = row.get(field_name, '')
            if not val:
                return default
            try:
                return float(val)
            except ValueError:
                return default

        def parse_int(field_name: str, default=0):
            val = row.get(field_name, '')
            if not val:
                return default
            try:
                return int(float(val))
            except ValueError:
                return default
        
        # Mapping based on CsvHeaders in gmaps/entry.go
        return cls(
            input_id=row.get('input_id', ''),
            link=row.get('link', ''),
            title=row.get('title', ''),
            category=row.get('category', ''),
            address=row.get('address', ''),
            open_hours=parse_json('open_hours', {}),
            popular_times=parse_json('popular_times', {}),
            web_site=row.get('website', ''),
            phone=row.get('phone', ''),
            plus_code=row.get('plus_code', ''),
            review_count=parse_int('review_count'),
            review_rating=parse_float('review_rating'),
            reviews_per_rating=parse_json('reviews_per_rating', {}),
            latitude=parse_float('latitude'),
            longitude=parse_float('longitude'),
            cid=row.get('cid', ''),
            status=row.get('status', ''),
            description=row.get('descriptions', ''), # Note: CSV header says 'descriptions'
            reviews_link=row.get('reviews_link', ''),
            thumbnail=row.get('thumbnail', ''),
            timezone=row.get('timezone', ''),
            price_range=row.get('price_range', ''),
            data_id=row.get('data_id', ''),
            place_id=row.get('place_id', ''),
            images=parse_json('images', []),
            reservations=parse_json('reservations', []),
            order_online=parse_json('order_online', []),
            menu=parse_json('menu', {}),
            owner=parse_json('owner', {}),
            complete_address=parse_json('complete_address', {}),
            about=parse_json('about', []),
            user_reviews=parse_json('user_reviews', []),
            emails=parse_json('emails', []) if row.get('emails') and row.get('emails').startswith('[') else ([x.strip() for x in row.get('emails', '').split(',')] if row.get('emails') else [])
        )


class MapsScraperClient:
    def __init__(self, base_url: str, timeout_s: int = 300) -> None:
        self.base_url = base_url.rstrip('/')
        self.timeout_s = timeout_s

    async def submit_job(
        self,
        query: str,
        *,
        lang: str = "en",
        zoom: int = 15,
        radius: int = 5000,
        depth: int = 1,
        fast_mode: bool = False,
        lat: str = "0",
        lon: str = "0",
        max_time: int = 180,
        proxies: Optional[List[str]] = None
    ) -> str:
        url = f"{self.base_url}/api/v1/jobs"
        real_payload = {
            "name": f"Job for {query}",
            "keywords": [query],
            "lang": lang,
            "zoom": zoom,
            "radius": radius,
            "depth": depth,
            "fast_mode": fast_mode,
            "lat": lat,
            "lon": lon,
            "max_time": max_time,
            "email": False,
        }
        
        if proxies:
            real_payload["proxies"] = proxies
            
        async with aiohttp.ClientSession() as session:
            async with session.post(url, json=real_payload, timeout=30) as response:
                if response.status != 201:
                    text = await response.text()
                    raise RuntimeError(f"Failed to submit job: {response.status} {text}")
                data = await response.json()
                return data['id']

    async def wait_for_job(self, job_id: str, *, poll_s: int = 5) -> Dict[str, Any]:
        url = f"{self.base_url}/api/v1/jobs/{job_id}"
        start_time = time.time()
        
        async with aiohttp.ClientSession() as session:
            while True:
                if time.time() - start_time > self.timeout_s:
                    raise TimeoutError(f"Job {job_id} timed out")
                
                async with session.get(url) as response:
                    if response.status == 404:
                         pass
                    elif response.status != 200:
                         logger.warning(f"Error checking job status: {response.status}")
                    else:
                        data = await response.json()
                        status = data.get('status')
                        if status == 'finished' or status == 'ok': # web.StatusOK is "ok"?
                             # web.go: job.Status = web.StatusOK
                             # In web/job.go, let's check values.
                             return data
                        if status == 'failed' or status == 'error':
                            raise RuntimeError(f"Job {job_id} failed: {data.get('error')}")
                
                await asyncio.sleep(poll_s)

    async def download_csv(self, job_id: str) -> str:
        url = f"{self.base_url}/api/v1/jobs/{job_id}/download"
        async with aiohttp.ClientSession() as session:
            async with session.get(url) as response:
                response.raise_for_status()
                return await response.text()

    async def fetch_places(self, query: str, **kwargs) -> List[MapsResult]:
        job_id = await self.submit_job(query, **kwargs)
        logger.info(f"Job submitted: {job_id}")
        
        job_details = await self.wait_for_job(job_id)
        logger.info(f"Job finished: {job_details.get('status')}")
        
        csv_content = await self.download_csv(job_id)
        
        results = []
        reader = csv.DictReader(io.StringIO(csv_content))
        for row in reader:
            results.append(MapsResult.from_csv_row(row))
            
        return results

async def main():
    if not aiohttp:
        print("aiohttp not installed, skipping execution")
        return

    client = MapsScraperClient("http://localhost:8090")
    
    # Example usage
    try:
        results = await client.fetch_places(
            "coffee shops in New York",
            zoom=15,
            radius=1000,
            depth=1,
            fast_mode=True
        )
        print(f"Found {len(results)} places")
        if results:
            print(f"First place: {results[0].title} - {results[0].address}")
    except Exception as e:
        print(f"Error: {e}")

if __name__ == "__main__":
    if aiohttp:
        asyncio.run(main())
    else:
        print("This script requires aiohttp to run. Please install it with 'pip install aiohttp'.")
