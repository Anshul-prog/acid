package lsd.mapreduce;

import java.io.IOException;
import java.util.HashSet;
import org.apache.hadoop.io.Text;
import org.apache.hadoop.mapreduce.Reducer;

public class InvertedIndexReducer extends Reducer<Text, Text, Text, Text> {
    private Text result = new Text();

    public void reduce(Text key, Iterable<Text> values, Context context) throws IOException, InterruptedException {
        // Collect distinct EntityIDs for the token
        HashSet<String> uniqueIds = new HashSet<>();
        for (Text val : values) {
            uniqueIds.add(val.toString());
        }
        
        // Join with commas
        result.set(String.join(",", uniqueIds));
        
        // Output format: <token, "id1,id2,id3">
        context.write(key, result);
    }
}
