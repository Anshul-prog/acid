package lsd.mapreduce;

import java.io.IOException;
import java.util.StringTokenizer;
import org.apache.hadoop.io.Text;
import org.apache.hadoop.mapreduce.Mapper;

public class InvertedIndexMapper extends Mapper<Object, Text, Text, Text> {
    private Text word = new Text();
    private Text documentId = new Text();

    public void map(Object key, Text value, Context context) throws IOException, InterruptedException {
        // Assume TSV format: EntityID \t FirstName \t LastName \t Remakrs
        String[] columns = value.toString().split("\t");
        if (columns.length < 2) return;
        
        String entityIdStr = columns[0];
        documentId.set(entityIdStr);
        
        // Tokenize remaining columns
        StringBuilder content = new StringBuilder();
        for(int i=1; i<columns.length; i++) {
            content.append(columns[i]).append(" ");
        }
        
        StringTokenizer itr = new StringTokenizer(content.toString().toLowerCase().replaceAll("[^a-z0-9]", " "));
        while (itr.hasMoreTokens()) {
            word.set(itr.nextToken());
            // Output format: <token, entityID>
            context.write(word, documentId);
        }
    }
}
